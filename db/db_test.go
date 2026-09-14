package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "omabudget", "ledger.db")
	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, path
}

func TestOpenMigratesToLatest(t *testing.T) {
	d, _ := openTemp(t)
	v, err := d.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := migrations[len(migrations)-1].version; v != want {
		t.Errorf("version = %d, want %d", v, want)
	}

	for _, table := range []string{
		"accounts", "categories", "payees", "payee_aliases", "tags", "transactions",
		"splits", "transaction_tags", "split_tags", "budgets", "recurring_rules", "fx_rates",
	} {
		var name string
		err := d.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

// TestFileModeIsPrivate: the ledger holds a financial record and must be
// created 0600 before SQLite writes a byte, so the -wal and -shm companions
// inherit the same mode.
func TestFileModeIsPrivate(t *testing.T) {
	_, path := openTemp(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %04o, want 0600", perm)
	}
}

func TestPragmas(t *testing.T) {
	d, _ := openTemp(t)
	var fk int
	if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d (err %v), want 1", fk, err)
	}
	var mode string
	if err := d.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode = %q (err %v), want wal", mode, err)
	}
}

// TestSeedIsIdempotent: opening the same ledger twice must not duplicate the
// taxonomy or fail on it, and a user's rename must survive, since ids are
// slugs of the original name rather than of the current one.
func TestSeedIsIdempotent(t *testing.T) {
	d, path := openTemp(t)
	count := func(db *DB) int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	want := len(systemCategories) + len(seedGroups)
	for _, g := range seedGroups {
		want += len(g.children)
	}
	if got := count(d); got != want {
		t.Fatalf("seeded %d categories, want %d", got, want)
	}

	if _, err := d.Exec(`UPDATE categories SET name='Lebensmittel' WHERE id='food/groceries'`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	again, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := count(again); got != want {
		t.Errorf("after reopen %d categories, want %d", got, want)
	}
	var name string
	if err := again.QueryRow(`SELECT name FROM categories WHERE id='food/groceries'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Lebensmittel" {
		t.Errorf("rename lost on reopen: %q", name)
	}
}

func TestSystemCategoriesAreMarked(t *testing.T) {
	d, _ := openTemp(t)
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM categories WHERE is_system=1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(systemCategories) {
		t.Errorf("%d system categories, want %d", n, len(systemCategories))
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM categories WHERE exclude_from_statistics=1 AND id LIKE 'savings-investments%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("savings categories should be excluded from statistics, spec 2.12")
	}
}

// TestConstraints: the rules spec 11 says block save on are enforced by the
// schema too, so no code path can write a row that breaks them.
func TestConstraints(t *testing.T) {
	d, _ := openTemp(t)
	now := "2026-09-13T00:00:00Z"
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	mustExec(`INSERT INTO accounts (id,name,type,currency,opening_date,created_at,modified_at) VALUES ('a','Main','checking','EUR','2026-01-01',?,?)`, now, now)
	mustExec(`INSERT INTO accounts (id,name,type,currency,opening_date,created_at,modified_at) VALUES ('b','Savings','savings','EUR','2026-01-01',?,?)`, now, now)

	cases := []struct {
		name string
		sql  string
	}{
		{"zero amount", `INSERT INTO transactions (id,kind,date,amount,currency,base_amount,account_id,category_id,created_at,modified_at)
			VALUES ('t1','expense','2026-09-13',0,'EUR',0,'a','food/groceries',?,?)`},
		{"transfer with a category", `INSERT INTO transactions (id,kind,date,amount,currency,base_amount,account_id,counter_account_id,category_id,created_at,modified_at)
			VALUES ('t2','transfer','2026-09-13',-100,'EUR',-100,'a','b','food/groceries',?,?)`},
		{"transfer to itself", `INSERT INTO transactions (id,kind,date,amount,currency,base_amount,account_id,counter_account_id,created_at,modified_at)
			VALUES ('t3','transfer','2026-09-13',-100,'EUR',-100,'a','a',?,?)`},
		{"transfer without a destination", `INSERT INTO transactions (id,kind,date,amount,currency,base_amount,account_id,created_at,modified_at)
			VALUES ('t4','transfer','2026-09-13',-100,'EUR',-100,'a',?,?)`},
		{"unknown category", `INSERT INTO transactions (id,kind,date,amount,currency,base_amount,account_id,category_id,created_at,modified_at)
			VALUES ('t5','expense','2026-09-13',-100,'EUR',-100,'a','no-such',?,?)`},
		{"unknown account type", `INSERT INTO accounts (id,name,type,currency,opening_date,created_at,modified_at) VALUES ('c','X','wallet','EUR','2026-01-01',?,?)`},
	}
	for _, c := range cases {
		if _, err := d.Exec(c.sql, now, now); err == nil {
			t.Errorf("%s: accepted, should be refused by the schema", c.name)
		}
	}

	// And the shapes that must be accepted.
	mustExec(`INSERT INTO transactions (id,kind,date,amount,currency,base_amount,account_id,category_id,created_at,modified_at)
		VALUES ('ok1','expense','2026-09-13',-450,'EUR',-450,'a','food/groceries',?,?)`, now, now)
	mustExec(`INSERT INTO transactions (id,kind,date,amount,currency,base_amount,account_id,counter_account_id,created_at,modified_at)
		VALUES ('ok2','transfer','2026-09-13',-50000,'EUR',-50000,'a','b',?,?)`, now, now)
}

// TestSeededTaxonomy pins the corners of the taxonomy a ledger is born with:
// the groups that were once bolted on afterwards are part of the structure
// now, and the one they replaced is gone.
func TestSeededTaxonomy(t *testing.T) {
	ctx := context.Background()
	d, _ := openTemp(t)

	for _, c := range []struct{ id, name, parent string }{
		{"pets", "Pets", ""},
		{"pets/veterinary", "Veterinary", "pets"},
		{"travel/fuel-charging", "Fuel & charging", "travel"},
	} {
		var name, parent string
		err := d.QueryRowContext(ctx,
			`SELECT name, COALESCE(parent_id,'') FROM categories WHERE id=?`, c.id).Scan(&name, &parent)
		if err != nil {
			t.Errorf("%s is missing: %v", c.id, err)
			continue
		}
		if name != c.name || parent != c.parent {
			t.Errorf("%s is %q under %q, want %q under %q", c.id, name, parent, c.name, c.parent)
		}
	}

	// Veterinary belongs to Pets, and only there.
	var strays int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM categories WHERE id='health/veterinary'`).Scan(&strays); err != nil {
		t.Fatal(err)
	}
	if strays != 0 {
		t.Error("Veterinary is still under Health")
	}

	// The goal columns are part of the table rather than added to it.
	var target int64
	var due string
	if err := d.QueryRowContext(ctx,
		`SELECT goal_target, goal_due FROM categories WHERE id='pets'`).Scan(&target, &due); err != nil {
		t.Fatalf("goal columns missing: %v", err)
	}
	if target != 0 || due != "" {
		t.Errorf("goal defaults are %d %q", target, due)
	}
}
