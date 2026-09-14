package domain

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/karamble/omarchy-omabudget/db"
)

// spendingFixture is September's spending: 230.00 on groceries across a
// plain row, a split line and a split on a row that names Food itself,
// 4.50 on coffee, 40.00 on household, plus rows that must not count.
func spendingFixture(t *testing.T) *Ledger {
	t.Helper()
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 12000, CategoryID: "Groceries", Date: "2026-09-02"})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 450, CategoryID: "Coffee & snacks", Date: "2026-09-05"})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 10000, Date: "2026-09-07", Splits: []Split{
		{CategoryID: "food/groceries", Amount: 6000},
		{CategoryID: "food/household-chemicals-hygiene", Amount: 4000},
	}})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 5000, CategoryID: "Food", Date: "2026-09-08", Splits: []Split{
		{CategoryID: "food/groceries", Amount: 5000},
	}})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 5000, CategoryID: "Fuel", Date: "2026-08-31"})
	addOn(t, l, Transaction{Kind: Income, AccountID: main.ID, Amount: 300000, CategoryID: "Primary salary", Date: "2026-09-03"})
	gone := addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 99900, CategoryID: "Groceries", Date: "2026-09-09"})
	if err := l.SoftDelete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	return l
}

// TestSpendingByCategory is the attribution rule: a row without splits
// counts under its own category, a row with splits counts per line and
// nothing under its own, and parents hold only their own lines.
func TestSpendingByCategory(t *testing.T) {
	l := spendingFixture(t)
	got, err := l.SpendingByCategory(context.Background(), "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		"food/groceries":                   23000,
		"food/coffee-snacks":               450,
		"food/household-chemicals-hygiene": 4000,
	}
	if !maps.Equal(got, want) {
		t.Errorf("spending = %v, want %v", got, want)
	}
	if _, err := l.SpendingByCategory(context.Background(), "2026-09-30", "2026-09-01"); err == nil {
		t.Error("reversed range accepted")
	}
}

// TestBudgets: a budget on a parent picks up its children, cards sort by
// percentage used then name, and the states follow the thresholds.
func TestBudgets(t *testing.T) {
	l := spendingFixture(t)
	ctx := context.Background()
	for _, b := range []struct {
		ref     string
		planned int64
	}{
		{"Food", 30000}, {"Groceries", 20000}, {"Coffee & snacks", 400}, {"Fuel", 10000}, {"Rent", 10000},
	} {
		if err := l.SetBudget(ctx, b.ref, "2026-09", b.planned); err != nil {
			t.Fatalf("SetBudget(%s): %v", b.ref, err)
		}
	}
	got, err := l.Budgets(ctx, "2026-09", "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	want := Budget{Period: "2026-09", Planned: 70400, Spent: 50900, Pct: 72, Cards: []BudgetCard{
		{"food/groceries", "Groceries", "󰄛", 20000, 23000, -3000, 115, BudgetOver},
		{"food/coffee-snacks", "Coffee & snacks", "󰄛", 400, 450, -50, 112, BudgetOver},
		{"food", "Food", "󰄛", 30000, 27450, 2550, 91, BudgetNear},
		{"transport/fuel", "Fuel", "󰄋", 10000, 0, 10000, 0, BudgetOK},
		{"housing/rent", "Rent", "󰋜", 10000, 0, 10000, 0, BudgetOK},
	}, Unbudgeted: []BudgetCard{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Budgets =\n%+v\nwant\n%+v", got, want)
	}

	// Spending is read over the range given, so another window shows none.
	august, err := l.Budgets(ctx, "2026-09", "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	if august.Spent != 5000 || august.Cards[0].CategoryID != "transport/fuel" || august.Cards[0].Pct != 50 {
		t.Errorf("august window = %+v", august)
	}

	// An empty period is a document with no cards, not null.
	empty, err := l.Budgets(ctx, "2026-10", "2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Cards == nil || len(empty.Cards) != 0 || empty.Planned != 0 || empty.Pct != 0 {
		t.Errorf("empty period = %+v", empty)
	}
	if _, err := l.Budgets(ctx, "sept", "2026-09-01", "2026-09-30"); err == nil {
		t.Error("bad period key accepted")
	}
}

func TestSetBudget(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	count := func() int {
		t.Helper()
		var n int
		if err := l.db.QueryRow(`SELECT COUNT(*) FROM budgets WHERE period='2026-09'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	planned := func(ref string) int64 {
		t.Helper()
		b, err := l.Budgets(ctx, "2026-09", "2026-09-01", "2026-09-30")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range b.Cards {
			if c.CategoryID == ref {
				return c.Planned
			}
		}
		return -1
	}

	if err := l.SetBudget(ctx, "Rent", "2026-09", 10000); err != nil {
		t.Fatal(err)
	}
	if err := l.SetBudget(ctx, "Fuel", "2026-09", 5000); err != nil {
		t.Fatal(err)
	}
	if count() != 2 || planned("housing/rent") != 10000 {
		t.Fatalf("after two sets: %d rows, rent %d", count(), planned("housing/rent"))
	}
	// Setting again replaces rather than duplicates.
	if err := l.SetBudget(ctx, "housing/rent", "2026-09", 12000); err != nil {
		t.Fatal(err)
	}
	if count() != 2 || planned("housing/rent") != 12000 {
		t.Errorf("after replacing: %d rows, rent %d", count(), planned("housing/rent"))
	}
	// Zero removes the line, and removing an absent line is not an error.
	if err := l.SetBudget(ctx, "Fuel", "2026-09", 0); err != nil {
		t.Fatal(err)
	}
	if count() != 1 || planned("transport/fuel") != -1 {
		t.Errorf("after zero: %d rows, fuel %d", count(), planned("transport/fuel"))
	}
	if err := l.SetBudget(ctx, "Fuel", "2026-09", 0); err != nil {
		t.Errorf("zero on an absent line: %v", err)
	}
	// Another period is its own plan.
	if err := l.SetBudget(ctx, "Rent", "2026-10", 999); err != nil {
		t.Fatal(err)
	}
	if count() != 1 || planned("housing/rent") != 12000 {
		t.Errorf("october leaked into september")
	}

	if _, err := l.db.Exec(`UPDATE categories SET is_archived=1 WHERE id='transport/bicycle'`); err != nil {
		t.Fatal(err)
	}
	refused := []struct {
		name    string
		ref     string
		period  string
		planned int64
		want    string
	}{
		{"negative", "Rent", "2026-09", -1, "negative"},
		{"income category", "Primary salary", "2026-09", 100, "income category"},
		{"archived", "transport/bicycle", "2026-09", 100, "archived"},
		{"bad period", "Rent", "2026/09", 100, "YYYY-MM"},
		{"unknown", "Nonsense", "2026-09", 100, "not found"},
		{"transfer marker", "sys-transfer", "2026-09", 100, "system category"},
		{"uncategorised marker", "sys-uncategorised", "2026-09", 100, "system category"},
		{"adjustment marker", "sys-balance-adjustment", "2026-09", 100, "system category"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			err := l.SetBudget(ctx, c.ref, c.period, c.planned)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("SetBudget(%s, %s, %d) = %v, want an error mentioning %q", c.ref, c.period, c.planned, err, c.want)
			}
			if c.name == "unknown" && !errors.Is(err, db.ErrNotFound) {
				t.Errorf("unknown category should be not found, got %v", err)
			}
		})
	}
	if count() != 1 {
		t.Errorf("a refused set changed the table: %d rows", count())
	}
}
