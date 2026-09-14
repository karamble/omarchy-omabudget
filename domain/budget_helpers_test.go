package domain

import (
	"context"
	"testing"
)

func TestRoundDiv(t *testing.T) {
	cases := []struct{ a, b, want int64 }{{10, 4, 3}, {9, 4, 2}, {-10, 4, -3}, {-9, 4, -2}, {7, 2, 4}, {0, 3, 0}, {5, 0, 0}}
	for _, c := range cases {
		if got := roundDiv(c.a, c.b); got != c.want {
			t.Errorf("%d/%d: got %d want %d", c.a, c.b, got, c.want)
		}
	}
}

func spend(t *testing.T, l *Ledger, a Account, cat, date string, minor int64) {
	t.Helper()
	c := mustCategory(t, l, cat)
	if _, err := l.Add(context.Background(), Transaction{Kind: Expense, AccountID: a.ID, Amount: minor, CategoryID: c.ID, Date: date}); err != nil {
		t.Fatal(err)
	}
}

func planned(t *testing.T, l *Ledger, key, cat string) int64 {
	t.Helper()
	c := mustCategory(t, l, cat)
	var v int64
	err := l.db.QueryRowContext(context.Background(), `SELECT planned FROM budgets WHERE period=? AND category_id=?`, key, c.ID).Scan(&v)
	if err != nil {
		return 0
	}
	return v
}

func TestBudgetHelpers(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 1000000)
	// Three past months of groceries: 300, 400, 500; fuel only in one.
	spend(t, l, a, "Groceries", "2026-06-10", 30000)
	spend(t, l, a, "Groceries", "2026-07-10", 40000)
	spend(t, l, a, "Groceries", "2026-08-10", 50000)
	spend(t, l, a, "Fuel", "2026-08-12", 6000)
	if err := l.SetBudget(ctx, "Groceries", "2026-09", 35000); err != nil {
		t.Fatal(err)
	}
	if err := l.SetBudget(ctx, "Fuel", "2026-09", 10000); err != nil {
		t.Fatal(err)
	}

	n, err := l.CopyBudget(ctx, "2026-09", "2026-10")
	if err != nil || n != 2 {
		t.Fatalf("copy: %d %v", n, err)
	}
	if planned(t, l, "2026-10", "Groceries") != 35000 || planned(t, l, "2026-10", "Fuel") != 10000 {
		t.Fatal("copy did not carry the lines")
	}
	if _, err := l.CopyBudget(ctx, "2026-10", "2026-10"); err == nil {
		t.Fatal("copied a period onto itself")
	}

	ranges := [][2]string{{"2026-06-01", "2026-06-30"}, {"2026-07-01", "2026-07-31"}, {"2026-08-01", "2026-08-31"}}
	n, err = l.PlanFromHistory(ctx, "2026-09", ranges, PlanAverage, "")
	if err != nil || n != 2 {
		t.Fatalf("average: %d %v", n, err)
	}
	if got := planned(t, l, "2026-09", "Groceries"); got != 40000 {
		t.Fatalf("average groceries %d", got)
	}
	if got := planned(t, l, "2026-09", "Fuel"); got != 2000 {
		t.Fatalf("average fuel %d", got)
	}
	if _, err := l.PlanFromHistory(ctx, "2026-09", ranges, PlanMedian, "Fuel"); err != nil {
		t.Fatal(err)
	}
	if got := planned(t, l, "2026-09", "Fuel"); got != 0 {
		t.Fatalf("median of 0, 0, 60 is 0, the line goes: got %d", got)
	}
	// A parent plans from its children's spending.
	if _, err := l.PlanFromHistory(ctx, "2026-09", ranges, PlanMedian, "Food"); err != nil {
		t.Fatal(err)
	}
	if got := planned(t, l, "2026-09", "Food"); got != 40000 {
		t.Fatalf("median food %d", got)
	}
	if _, err := l.PlanFromHistory(ctx, "2026-09", ranges, "mode", ""); err == nil {
		t.Fatal("unknown method accepted")
	}

	n, err = l.ScaleBudget(ctx, "2026-09", 5)
	if err != nil || n != 2 {
		t.Fatalf("scale: %d %v", n, err)
	}
	if got := planned(t, l, "2026-09", "Groceries"); got != 42000 {
		t.Fatalf("scaled groceries %d", got)
	}
	if got := planned(t, l, "2026-09", "Food"); got != 42000 {
		t.Fatalf("scaled food %d", got)
	}
	if _, err := l.ScaleBudget(ctx, "2026-09", -100); err == nil {
		t.Fatal("scaled to nothing")
	}

	// The unbudgeted list: spending with no plan, a child hidden by its
	// planned parent.
	spend(t, l, a, "Coffee & snacks", "2026-09-03", 450)
	spend(t, l, a, "Fuel", "2026-09-04", 7000)
	b, err := l.Budgets(ctx, "2026-09", "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range b.Unbudgeted {
		names = append(names, u.Name)
	}
	if len(names) != 1 || names[0] != "Fuel" {
		t.Fatalf("unbudgeted %v (coffee sits under the planned Food)", names)
	}
}
