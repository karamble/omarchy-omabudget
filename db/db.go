// Package db opens the ledger and brings its schema up to date.
//
// SQLite through modernc.org/sqlite, which is pure Go, so CGO stays off and
// the binary does not vary with whether a C compiler is present. The database
// lives in the plugin's config directory and nowhere else: SQLite over a
// syncing folder corrupts, so keeping a copy elsewhere is an export, not a
// path.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB is the open ledger.
type DB struct {
	*sql.DB
	path string
}

// Open creates the file if needed, at mode 0600 before SQLite ever writes to
// it, then opens it with the pragmas a single-user ledger wants and applies
// any migrations that have not run yet.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	// Create with the right mode up front. SQLite makes its -wal and -shm
	// companions with the main file's permissions, so getting this one right
	// covers all three.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", path, err)
	}
	f.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}

	// WAL for concurrent reads while the daemon writes; foreign keys because
	// a category referenced by a transaction must not silently vanish;
	// busy_timeout so a second connection waits instead of failing.
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	// One writer. SQLite serialises writes anyway; letting database/sql open
	// more connections only turns that into busy errors.
	sqldb.SetMaxOpenConns(1)

	d := &DB{DB: sqldb, path: path}
	if err := d.migrate(ctx); err != nil {
		sqldb.Close()
		return nil, err
	}
	return d, nil
}

// Path reports where the ledger lives.
func (d *DB) Path() string { return d.path }

// Version reports the schema version the ledger is at.
func (d *DB) Version(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := d.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// migrate applies every migration above the recorded version, each in its own
// transaction, so a failure leaves the ledger at a known version rather than
// half way through one.
func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("preparing migrations: %w", err)
	}
	current, err := d.Version(ctx)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := d.apply(ctx, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func (d *DB) apply(ctx context.Context, m migration) (err error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, stmt := range m.statements {
		if _, err = tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w\nin: %s", err, firstLine(stmt))
		}
	}
	if m.after != nil {
		if err = m.after(ctx, tx); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
		m.version); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrNotFound is returned by lookups that matched nothing.
var ErrNotFound = errors.New("not found")

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

// SnapshotInto writes a consistent copy of the database into path, which
// must exist and be empty. The copy is a plain database file without the
// write-ahead log.
func (d *DB) SnapshotInto(ctx context.Context, path string) error {
	_, err := d.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}
