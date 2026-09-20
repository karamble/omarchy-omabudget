package domain

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/money"
)

func newLedger(t *testing.T) *Ledger {
	t.Helper()
	return newLedgerWith(t, "EUR")
}

// newLedgerWith opens a ledger whose rates are quoted against reference.
func newLedgerWith(t *testing.T, reference string) *Ledger {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "ledger.db"), db.Options{RateReference: reference})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	l := New(d)
	l.now = func() time.Time { return time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC) }
	return l
}

func mustAccount(t *testing.T, l *Ledger, name string, typ AccountType, currency string, opening int64) Account {
	t.Helper()
	a, err := l.AddAccount(context.Background(), Account{
		Name: name, Type: typ, Currency: currency, OpeningBalance: opening,
		OpeningDate: "2026-01-01", IncludeInNetWorth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func balance(t *testing.T, l *Ledger, id string) int64 {
	t.Helper()
	b, err := l.Balance(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return b.Minor
}

func TestAccountValidation(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	cases := []struct {
		name string
		a    Account
	}{
		{"no name", Account{Type: Checking, Currency: "EUR"}},
		{"bad type", Account{Name: "x", Type: "wallet", Currency: "EUR"}},
		{"bad currency", Account{Name: "x", Type: Checking, Currency: "euro"}},
		{"bad date", Account{Name: "x", Type: Checking, Currency: "EUR", OpeningDate: "13/09/2026"}},
	}
	for _, c := range cases {
		if _, err := l.AddAccount(ctx, c.a); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	a, err := l.AddAccount(ctx, Account{Name: "Main", Type: Checking, Currency: "eur"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Currency != "EUR" || a.OpeningDate != "2026-09-13" || !a.Active {
		t.Errorf("defaults not applied: %+v", a)
	}
}

// TestExpenseAndIncome: signs are normalised from the kind, so a person can
// type 4.50 for a coffee and it leaves the account; and a row in the
// reference currency is at par, with no rate of its own.
func TestExpenseAndIncome(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)

	coffee, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: main.ID, Amount: 450, CategoryID: "Coffee & snacks"})
	if err != nil {
		t.Fatal(err)
	}
	if coffee.Amount != -450 || coffee.ReferenceAmount != -450 || coffee.FXRate != "" {
		t.Errorf("expense = %+v", coffee)
	}
	if coffee.CategoryID != "food/coffee-snacks" {
		t.Errorf("category resolved to %q", coffee.CategoryID)
	}
	if coffee.Date != "2026-09-13" || coffee.Status != Cleared {
		t.Errorf("defaults: date %q status %q", coffee.Date, coffee.Status)
	}

	pay, err := l.Add(ctx, Transaction{Kind: Income, AccountID: main.ID, Amount: -325000, CategoryID: "Primary salary"})
	if err != nil {
		t.Fatal(err)
	}
	if pay.Amount != 325000 {
		t.Errorf("income sign not normalised: %d", pay.Amount)
	}

	if got := balance(t, l, main.ID); got != 100000-450+325000 {
		t.Errorf("balance = %d", got)
	}
}

func TestCategoryKindMustMatchDirection(t *testing.T) {
	l := newLedger(t)
	main := mustAccount(t, l, "Main", Checking, "EUR", 0)
	_, err := l.Add(context.Background(), Transaction{Kind: Expense, AccountID: main.ID, Amount: 100, CategoryID: "Primary salary"})
	if err == nil || !strings.Contains(err.Error(), "income category") {
		t.Errorf("expense on an income category should be refused, got %v", err)
	}
}

func TestUncategorisedDefault(t *testing.T) {
	l := newLedger(t)
	main := mustAccount(t, l, "Main", Checking, "EUR", 0)
	tx, err := l.Add(context.Background(), Transaction{Kind: Expense, AccountID: main.ID, Amount: 100})
	if err != nil {
		t.Fatal(err)
	}
	if tx.CategoryID != "sys-uncategorised" {
		t.Errorf("category = %q, want sys-uncategorised", tx.CategoryID)
	}
}

// TestTransfer: one object, both ledgers move, no category, and P2 holds:
// nothing about it is income or expense.
func TestTransfer(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	save := mustAccount(t, l, "Savings", Savings, "EUR", 0)

	tx, err := l.Add(ctx, Transaction{Kind: Transfer, AccountID: main.ID, CounterAccountID: "savings", Amount: 50000, CategoryID: "Groceries"})
	if err != nil {
		t.Fatal(err)
	}
	if tx.CategoryID != "" {
		t.Errorf("transfer kept a category: %q", tx.CategoryID)
	}
	if tx.CounterAccountID != save.ID {
		t.Errorf("counter account resolved to %q", tx.CounterAccountID)
	}
	if got := balance(t, l, main.ID); got != 50000 {
		t.Errorf("source balance = %d, want 50000", got)
	}
	if got := balance(t, l, save.ID); got != 50000 {
		t.Errorf("destination balance = %d, want 50000", got)
	}

	if _, err := l.Add(ctx, Transaction{Kind: Transfer, AccountID: main.ID, CounterAccountID: main.ID, Amount: 100}); err == nil {
		t.Error("transfer to itself accepted")
	}
	if _, err := l.Add(ctx, Transaction{Kind: Transfer, AccountID: main.ID, Amount: 100}); err == nil {
		t.Error("transfer without a destination accepted")
	}
}

// TestCrossCurrencyTransfer: both legs recorded as entered, spec 4.3.
func TestCrossCurrencyTransfer(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	eur := mustAccount(t, l, "EUR", Checking, "EUR", 100000)
	pln := mustAccount(t, l, "PLN", Checking, "PLN", 0)

	if _, err := l.Add(ctx, Transaction{Kind: Transfer, AccountID: eur.ID, CounterAccountID: pln.ID, Amount: 10000}); err == nil {
		t.Fatal("cross-currency transfer without the received amount was accepted")
	}
	received := int64(43125) // 431.25 PLN
	if _, err := l.Add(ctx, Transaction{Kind: Transfer, AccountID: eur.ID, CounterAccountID: pln.ID, Amount: 10000, CounterAmount: &received}); err != nil {
		t.Fatal(err)
	}
	if got := balance(t, l, eur.ID); got != 90000 {
		t.Errorf("EUR balance = %d", got)
	}
	if got := balance(t, l, pln.ID); got != 43125 {
		t.Errorf("PLN balance = %d, want 43125", got)
	}
}

// TestSplits: lines must sum to the parent exactly, and the error names the
// remainder, which is what the form shows while they do not.
func TestSplits(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)

	_, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: main.ID, Amount: 25500, Splits: []Split{
		{CategoryID: "food/groceries", Amount: 18000},
		{CategoryID: "food/household-chemicals-hygiene", Amount: 4000},
	}})
	if err == nil || !strings.Contains(err.Error(), "35.00 short") {
		t.Errorf("short split should name the remainder, got %v", err)
	}

	tx, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: main.ID, Amount: 25500, Splits: []Split{
		{CategoryID: "food/groceries", Amount: 18000},
		{CategoryID: "food/household-chemicals-hygiene", Amount: 4000},
		{CategoryID: "food/alcohol-tobacco", Amount: 3500},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, s := range tx.Splits {
		if s.Amount > 0 {
			t.Errorf("split line kept the wrong sign: %+v", s)
		}
		if s.ReferenceAmount != s.Amount {
			t.Errorf("split base amount %d != amount %d in base currency", s.ReferenceAmount, s.Amount)
		}
		sum += s.Amount
	}
	if sum != -25500 {
		t.Errorf("lines sum to %d", sum)
	}
	if got := balance(t, l, main.ID); got != 100000-25500 {
		t.Errorf("balance = %d", got)
	}
}

// TestEnteredRateBeatsTheTable is spec 8 at the ledger: a foreign-currency
// entry takes the rate it was given, else the table's for its date, and a
// later correction to the table moves only the rows that followed it.
func TestEnteredRateBeatsTheTable(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	pln := mustAccount(t, l, "PLN card", CreditCard, "PLN", 0)

	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: pln.ID, Amount: 10000, CategoryID: "Fuel"}); err == nil {
		t.Fatal("foreign-currency entry with no rate anywhere was accepted")
	}

	explicit, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: pln.ID, Amount: 10000, CategoryID: "Fuel", FXRate: "0.2320"})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.ReferenceAmount != -2320 {
		t.Errorf("base = %d, want -2320", explicit.ReferenceAmount)
	}

	if _, err := l.db.Exec(`INSERT INTO fx_rates (date,currency,rate) VALUES ('2026-09-01','PLN','0.2400')`); err != nil {
		t.Fatal(err)
	}
	fromTable, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: pln.ID, Amount: 10000, CategoryID: "Fuel"})
	if err != nil {
		t.Fatal(err)
	}
	if fromTable.FXRate != "" || fromTable.ReferenceAmount != -2400 {
		t.Errorf("rate table not used, or its rate was kept on the row: %+v", fromTable)
	}

	// The rate moves: the row that followed the table follows it, the row
	// with a rate of its own does not.
	if _, err := l.SetRate(ctx, "PLN", "0.9999", "2026-09-13"); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.Get(ctx, fromTable.ID); got.ReferenceAmount != -9999 || got.FXRate != "" {
		t.Errorf("the table row did not follow the correction: %+v", got)
	}
	if got, _ := l.Get(ctx, explicit.ID); got.ReferenceAmount != -2320 || got.FXRate != "0.2320" {
		t.Errorf("the entered rate moved: %+v", got)
	}
}

func TestSoftDeleteAndRestore(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 10000)
	tx, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: main.ID, Amount: 2500, CategoryID: "Groceries"})
	if err != nil {
		t.Fatal(err)
	}
	if got := balance(t, l, main.ID); got != 7500 {
		t.Fatalf("balance = %d", got)
	}
	if err := l.SoftDelete(ctx, tx.ID); err != nil {
		t.Fatal(err)
	}
	if got := balance(t, l, main.ID); got != 10000 {
		t.Errorf("deleted transaction still counted: %d", got)
	}
	if list, _ := l.Transactions(ctx, Filter{}); len(list) != 0 {
		t.Errorf("deleted transaction still listed")
	}
	if err := l.Restore(ctx, tx.ID); err != nil {
		t.Fatal(err)
	}
	if got := balance(t, l, main.ID); got != 7500 {
		t.Errorf("restored transaction not counted: %d", got)
	}
	if err := l.SoftDelete(ctx, "nope"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("deleting nothing: %v", err)
	}
}

func TestCategoryLookup(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	for _, ref := range []string{"food/groceries", "Groceries", "groceries", "Food / Groceries"} {
		c, err := l.Category(ctx, ref)
		if err != nil || c.ID != "food/groceries" {
			t.Errorf("Category(%q) = %+v, %v", ref, c, err)
		}
	}
	if _, err := l.Category(ctx, "Nonsense"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("unknown category: %v", err)
	}
	// "Transport (flights/rail)" under Travel and "Transport" the group share a
	// word but not a name, so this must resolve to the group, not be ambiguous.
	if c, err := l.Category(ctx, "Transport"); err != nil || c.ID != "transport" {
		t.Errorf("Category(Transport) = %+v, %v", c, err)
	}
	// Quick-add types "coffee"; the category is "Coffee & snacks". A unique
	// substring resolves, an ambiguous one is refused with the candidates named.
	if c, err := l.Category(ctx, "coffee"); err != nil || c.ID != "food/coffee-snacks" {
		t.Errorf("Category(coffee) = %+v, %v", c, err)
	}
	if _, err := l.Category(ctx, "insurance"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("insurance matches several categories and should be ambiguous, got %v", err)
	}
}

func TestTags(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 0)
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: main.ID, Amount: 100, CategoryID: "Groceries",
		Tags: []string{"#client-naturalspace", "renovation"}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := l.db.QueryRow(`SELECT COUNT(*) FROM tags`).Scan(&n); err != nil || n != 2 {
		t.Errorf("tags created = %d (err %v), want 2", n, err)
	}
	var name string
	if err := l.db.QueryRow(`SELECT name FROM tags WHERE name='client-naturalspace'`).Scan(&name); err != nil {
		t.Errorf("leading # should be stripped: %v", err)
	}
	// Same tag again is reused, not duplicated.
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: main.ID, Amount: 100, CategoryID: "Groceries",
		Tags: []string{"renovation"}}); err != nil {
		t.Fatal(err)
	}
	if err := l.db.QueryRow(`SELECT COUNT(*) FROM tags`).Scan(&n); err != nil || n != 2 {
		t.Errorf("tags after reuse = %d, want 2", n)
	}
}

func TestZeroAmountRefused(t *testing.T) {
	l := newLedger(t)
	main := mustAccount(t, l, "Main", Checking, "EUR", 0)
	if _, err := l.Add(context.Background(), Transaction{Kind: Expense, AccountID: main.ID, Amount: 0}); !errors.Is(err, money.ErrZero) {
		t.Errorf("zero amount: %v", err)
	}
}

// TestRemoveAccount pins the one path that makes a typo at creation fixable,
// and the refusal that keeps history safe.
func TestRemoveAccount(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	typo := mustAccount(t, l, "Savigns", Savings, "EUR", 0)
	used := mustAccount(t, l, "Checking", Checking, "EUR", 100000)

	if err := l.RemoveAccount(ctx, "Savigns"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Account(ctx, typo.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("the account is still there: %v", err)
	}

	groceries := mustCategory(t, l, "Groceries")
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: used.ID, CategoryID: groceries.ID,
		Amount: 4500, Date: "2026-09-02"}); err != nil {
		t.Fatal(err)
	}
	err := l.RemoveAccount(ctx, used.ID)
	if err == nil || !strings.Contains(err.Error(), "close it instead") {
		t.Fatalf("an account with a transaction was removed: %v", err)
	}

	// The other side of a transfer counts too.
	from := mustAccount(t, l, "Cash", Cash, "EUR", 50000)
	to := mustAccount(t, l, "Savings", Savings, "EUR", 0)
	if _, err := l.Add(ctx, Transaction{Kind: Transfer, AccountID: from.ID, CounterAccountID: to.ID,
		Amount: 10000, Date: "2026-09-03"}); err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveAccount(ctx, to.ID); err == nil {
		t.Fatal("the receiving side of a transfer was removed")
	}
}
