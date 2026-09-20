package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "omabudget", "ledger.db")
	d, err := Open(context.Background(), path, Options{RateReference: "EUR"})
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
		"splits", "transaction_tags", "split_tags", "budgets", "recurring_rules", "fx_rates", "meta",
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

	again, err := Open(context.Background(), path, Options{RateReference: "EUR"})
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

// TestRateReferenceIsPinned: the reference is written once, from the option
// given at the first open, and the ledger's own record wins on every open
// after that.
func TestRateReferenceIsPinned(t *testing.T) {
	ctx := context.Background()
	d, path := openTemp(t)
	if got := d.RateReference(); got != "EUR" {
		t.Fatalf("reference = %q, want EUR", got)
	}
	d.Close()

	again, err := Open(ctx, path, Options{RateReference: "PLN"})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := again.RateReference(); got != "EUR" {
		t.Errorf("reopening under another base moved the reference to %q", got)
	}
	if v, err := again.Meta(ctx, MetaRateReference); err != nil || v != "EUR" {
		t.Errorf("meta %s = %q, %v", MetaRateReference, v, err)
	}
	if _, err := again.Meta(ctx, "no-such-key"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key err = %v, want ErrNotFound", err)
	}
}

// TestRateReferenceIsRequired: a fresh ledger cannot be brought to version 2
// without knowing what its rates are quoted against.
func TestRateReferenceIsRequired(t *testing.T) {
	for _, ref := range []string{"", "EU", "euro"} {
		path := filepath.Join(t.TempDir(), "ledger.db")
		d, err := Open(context.Background(), path, Options{RateReference: ref})
		if err == nil {
			d.Close()
			t.Errorf("reference %q was accepted", ref)
		}
	}
}

// TestRefusesNewerSchema: a ledger written by a later build is refused rather
// than opened and misread.
func TestRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	d, path := openTemp(t)
	newest := migrations[len(migrations)-1].version
	if _, err := d.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, '2030-01-01T00:00:00Z')`, newest+1); err != nil {
		t.Fatal(err)
	}
	d.Close()

	again, err := Open(ctx, path, Options{RateReference: "EUR"})
	if err == nil {
		again.Close()
		t.Fatal("a newer ledger was opened")
	}
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("err = %v, want ErrSchemaTooNew", err)
	}
}

// TestPreRunsOutsideTransaction: the pre hook sees the bare connection, so
// it can do what a transaction forbids, and it runs before the statements.
func TestPreRunsOutsideTransaction(t *testing.T) {
	ctx := context.Background()
	d, _ := openTemp(t)
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	var order []string
	m := migration{
		version: 99,
		name:    "probe",
		pre: func(ctx context.Context, d *DB) error {
			order = append(order, "pre")
			// VACUUM INTO fails inside a transaction.
			return d.SnapshotInto(ctx, snapshot)
		},
		statements: []string{`CREATE TABLE probe (n INTEGER)`},
		after: func(ctx context.Context, tx *sql.Tx, opts Options) error {
			order = append(order, "after "+opts.RateReference)
			_, err := tx.ExecContext(ctx, `INSERT INTO probe (n) VALUES (1)`)
			return err
		},
	}
	if err := d.apply(ctx, m); err != nil {
		t.Fatal(err)
	}
	if want := []string{"pre", "after EUR"}; strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("hooks ran as %v, want %v", order, want)
	}
	if _, err := os.Stat(snapshot); err != nil {
		t.Errorf("pre did not write the snapshot: %v", err)
	}
	if v, err := d.Version(ctx); err != nil || v != 99 {
		t.Errorf("version = %d, %v; want 99", v, err)
	}
}

// TestFailedAfterRollsBack: a hook that fails leaves the ledger at the
// version before, with none of the migration's statements applied.
func TestFailedAfterRollsBack(t *testing.T) {
	ctx := context.Background()
	d, _ := openTemp(t)
	before, _ := d.Version(ctx)
	m := migration{
		version:    99,
		name:       "broken",
		statements: []string{`CREATE TABLE probe (n INTEGER)`},
		after: func(ctx context.Context, tx *sql.Tx, _ Options) error {
			return errors.New("no")
		},
	}
	if err := d.apply(ctx, m); err == nil {
		t.Fatal("a failing hook was applied")
	}
	if v, _ := d.Version(ctx); v != before {
		t.Errorf("version moved to %d", v)
	}
	var name string
	if err := d.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE name='probe'`).Scan(&name); err == nil {
		t.Error("the table from a rolled back migration is still there")
	}
}

// TestBlankDerivedRates: bringing a version 2 ledger forward clears the rate
// on every row that agrees with the table for its date, comparing as
// numbers, and on every row in the reference currency; a rate the table did
// not have stays on its row.
func TestBlankDerivedRates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.db")
	old, err := connect(path, Options{RateReference: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.migrateTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	now := "2026-09-13T00:00:00Z"
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := old.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	mustExec(`INSERT INTO accounts (id,name,type,currency,opening_date,created_at,modified_at) VALUES ('a','Main','checking','EUR','2026-01-01',?,?)`, now, now)
	mustExec(`INSERT INTO fx_rates (date,currency,rate) VALUES ('2026-01-01','PLN','0.235'), ('2026-03-01','PLN','0.24')`)
	for _, r := range []struct{ id, date, currency, rate string }{
		{"same", "2026-02-01", "PLN", "0.235"},
		{"same-zeros", "2026-02-01", "PLN", "0.2350"},
		{"typed", "2026-02-01", "PLN", "0.232"},
		{"later", "2026-03-05", "PLN", "0.24"},
		{"reference", "2026-02-01", "EUR", "1"},
		{"unquoted", "2026-02-01", "USD", "0.9"},
		{"before-first", "2025-12-01", "PLN", "0.235"},
	} {
		mustExec(`INSERT INTO transactions (id,kind,date,amount,currency,fx_rate,base_amount,account_id,category_id,created_at,modified_at)
			VALUES (?,'expense',?,-100,?,?,-23,'a','food/groceries',?,?)`, r.id, r.date, r.currency, r.rate, now, now)
	}
	old.Close()

	d, err := Open(ctx, path, Options{RateReference: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	want := map[string]string{
		"same": "", "same-zeros": "", "typed": "0.232", "later": "",
		"reference": "", "unquoted": "0.9", "before-first": "",
	}
	for id, rate := range want {
		var got string
		if err := d.QueryRowContext(ctx, `SELECT fx_rate FROM transactions WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != rate {
			t.Errorf("%s has rate %q, want %q", id, got, rate)
		}
	}
}

// TestFillBudgetCurrency: bringing a version 3 ledger forward stamps every
// budget line with the reference, and a line filed afterwards keeps the
// currency it is given.
func TestFillBudgetCurrency(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.db")
	old, err := connect(path, Options{RateReference: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.migrateTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := old.ExecContext(ctx, `INSERT INTO budgets (id,category_id,period,planned) VALUES ('b1','food/groceries','2026-09',30000)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	d, err := Open(ctx, path, Options{RateReference: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var currency string
	if err := d.QueryRowContext(ctx, `SELECT currency FROM budgets WHERE id='b1'`).Scan(&currency); err != nil {
		t.Fatal(err)
	}
	if currency != "EUR" {
		t.Errorf("backfilled currency = %q, want EUR", currency)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO budgets (id,category_id,period,planned,currency) VALUES ('b2','food/groceries','2026-10',40000,'PLN')`); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `SELECT currency FROM budgets WHERE id='b2'`).Scan(&currency); err != nil || currency != "PLN" {
		t.Errorf("filed currency = %q, %v; want PLN", currency, err)
	}
}

// TestLabelRateSources: bringing a version 4 ledger forward marks the rows
// the old seeding wrote as seed, leaves typed rows manual, and records a
// marker for every currency seeding offered, on file or since removed.
func TestLabelRateSources(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.db")
	old, err := connect(path, Options{RateReference: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.migrateTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	// The old seeding filed USD and PLN; PLN was removed and GBP typed.
	if _, err := old.ExecContext(ctx, `INSERT INTO fx_rates (date,currency,rate) VALUES
		('2026-01-01','USD','0.92'), ('2026-03-01','USD','0.9'), ('2026-02-01','GBP','1.15')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	d, err := Open(ctx, path, Options{RateReference: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	want := map[string]string{"2026-01-01 USD": "seed", "2026-03-01 USD": "manual", "2026-02-01 GBP": "manual"}
	rows, err := d.QueryContext(ctx, `SELECT date, currency, source FROM fx_rates`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for rows.Next() {
		var date, currency, source string
		if err := rows.Scan(&date, &currency, &source); err != nil {
			t.Fatal(err)
		}
		got[date+" "+currency] = source
	}
	rows.Close()
	for k, source := range want {
		if got[k] != source {
			t.Errorf("%s is %q, want %q", k, got[k], source)
		}
	}
	for _, currency := range []string{"USD", "PLN", "GBP"} {
		if _, err := d.Meta(ctx, MetaRateSeeded(currency)); err != nil {
			t.Errorf("no marker for %s: %v", currency, err)
		}
	}
	if _, err := d.Meta(ctx, MetaRateSeeded("EUR")); !errors.Is(err, ErrNotFound) {
		t.Errorf("the reference got a marker: %v", err)
	}
	var name string
	if err := d.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='index' AND name='fx_rates_currency_date'`).Scan(&name); err != nil {
		t.Errorf("index missing: %v", err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO fx_rates (date,currency,rate,source) VALUES ('2026-04-01','USD','0.9','guess')`); err == nil {
		t.Error("an unknown source was accepted")
	}
}

// TestFreshLedgerHasNoSeedMarkers: a ledger nothing has touched was never
// offered a starting rate, so it carries no marker to say otherwise.
func TestFreshLedgerHasNoSeedMarkers(t *testing.T) {
	d, _ := openTemp(t)
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM meta WHERE key LIKE 'rate_seeded:%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d markers on a fresh ledger", n)
	}
}
