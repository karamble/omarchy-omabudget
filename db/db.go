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
	"strings"

	_ "modernc.org/sqlite"
)

// DB is the open ledger.
type DB struct {
	*sql.DB
	path      string
	opts      Options
	reference string
}

// Options is what a migration needs from outside the ledger.
type Options struct {
	// RateReference is the currency every exchange rate is quoted against. It
	// is written into the ledger the first time it is brought to version 2
	// and never read from here again: the ledger's own record wins, so a
	// backup restored under another configuration keeps its rates meaningful.
	RateReference string
}

// Open creates the file if needed, at mode 0600 before SQLite ever writes to
// it, then opens it with the pragmas a single-user ledger wants and applies
// any migrations that have not run yet.
func Open(ctx context.Context, path string, opts Options) (*DB, error) {
	d, err := connect(path, opts)
	if err != nil {
		return nil, err
	}
	if err := d.migrate(ctx); err != nil {
		d.Close()
		return nil, err
	}
	if d.reference, err = d.Meta(ctx, MetaRateReference); err != nil {
		d.Close()
		return nil, fmt.Errorf("reading the rate reference: %w", err)
	}
	return d, nil
}

// connect creates and opens the file without touching its schema.
func connect(path string, opts Options) (*DB, error) {
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
	return &DB{DB: sqldb, path: path, opts: opts}, nil
}

// Path reports where the ledger lives.
func (d *DB) Path() string { return d.path }

// RateReference reports the currency the ledger's exchange rates are quoted
// against, as recorded in the ledger itself.
func (d *DB) RateReference() string { return d.reference }

// MetaRateReference is the meta key the rate reference is kept under.
const MetaRateReference = "rate_reference"

// MetaRateSeeded is the meta key recording that a starting rate has been
// offered for a currency. Its value is the date the rate was filed under.
func MetaRateSeeded(currency string) string {
	return "rate_seeded:" + strings.ToUpper(currency)
}

// Meta reads one value from the meta table; a key that is not there is
// ErrNotFound.
func (d *DB) Meta(ctx context.Context, key string) (string, error) {
	var value string
	err := d.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("meta %s: %w", key, ErrNotFound)
	}
	return value, err
}

// Version reports the schema version the ledger is at.
func (d *DB) Version(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := d.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// ErrSchemaTooNew is returned when the ledger was written by a newer build
// than this one. An older binary could read it, but not without misreading
// whatever the newer schema added.
var ErrSchemaTooNew = errors.New("ledger schema is newer than this build")

// migrate applies every migration above the recorded version, each in its own
// transaction, so a failure leaves the ledger at a known version rather than
// half way through one. A ledger already past the newest migration is refused
// rather than opened.
func (d *DB) migrate(ctx context.Context) error {
	return d.migrateTo(ctx, migrations[len(migrations)-1].version)
}

// migrateTo applies the migrations up to and including version, which is
// how a test builds the ledger an older build would have left behind.
func (d *DB) migrateTo(ctx context.Context, version int) error {
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
	if newest := migrations[len(migrations)-1].version; current > newest {
		return fmt.Errorf("%w: version %d, this build knows %d", ErrSchemaTooNew, current, newest)
	}
	for _, m := range migrations {
		if m.version <= current || m.version > version {
			continue
		}
		if err := d.apply(ctx, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

// apply runs one migration: pre on the bare connection, then the statements,
// after and the version stamp in one transaction.
func (d *DB) apply(ctx context.Context, m migration) (err error) {
	if m.pre != nil {
		if err := m.pre(ctx, d); err != nil {
			return err
		}
	}
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
		if err = m.after(ctx, tx, d.opts); err != nil {
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
