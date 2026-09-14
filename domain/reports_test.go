package domain

import (
	"context"
	"strings"
	"testing"
)

func TestSpendingReport(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 500000)
	spend(t, l, a, "Groceries", "2026-08-05", 30000)
	spend(t, l, a, "Coffee & snacks", "2026-08-06", 1000)
	spend(t, l, a, "Fuel", "2026-08-07", 5000)
	spend(t, l, a, "Groceries", "2026-09-05", 45000)
	spend(t, l, a, "Rent", "2026-09-03", 100000)
	if err := l.SetBudget(ctx, "Food", "2026-09", 40000); err != nil {
		t.Fatal(err)
	}

	rep, err := l.Spending(ctx, "2026-09-01", "2026-09-30", "2026-08-01", "2026-08-31", "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 145000 || rep.Previous != 36000 {
		t.Fatalf("totals %d %d", rep.Total, rep.Previous)
	}
	if len(rep.Rows) != 3 || rep.Rows[0].Name != "Housing" || rep.Rows[1].Name != "Food" || rep.Rows[2].Name != "Transport" {
		t.Fatalf("%+v", rep.Rows)
	}
	food := rep.Rows[1]
	if food.Spent != 45000 || food.Previous != 31000 || food.Share != 31 || food.Delta != 14000 || food.DeltaPct == nil || *food.DeltaPct != 45.1 || food.Planned != 40000 {
		t.Fatalf("%+v", food)
	}
	if len(food.Children) != 2 || food.Children[0].Name != "Groceries" || food.Children[1].Name != "Coffee & snacks" || food.Children[1].Spent != 0 {
		t.Fatalf("%+v", food.Children)
	}
	housing := rep.Rows[0]
	if housing.DeltaPct != nil || housing.Delta != 100000 || housing.Share != 68 {
		t.Fatalf("%+v", housing)
	}
	transport := rep.Rows[2]
	if transport.Spent != 0 || transport.Previous != 5000 || *transport.DeltaPct != -100 {
		t.Fatalf("%+v", transport)
	}
}

func TestMetrics(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 900000)
	// Three trailing periods at 300, 400, 500.
	spend(t, l, a, "Groceries", "2026-06-10", 30000)
	spend(t, l, a, "Groceries", "2026-07-10", 40000)
	spend(t, l, a, "Groceries", "2026-08-10", 50000)
	// This period: 140 in 14 days, 100 of it posted by a rule.
	spend(t, l, a, "Groceries", "2026-09-05", 4000)
	r := rent(t, l, a, "2026-09-03")
	if _, err := l.Post(ctx, r.ID, "", 0); err != nil {
		t.Fatal(err)
	}
	// Rent is 1200 a month; that instance is 120000, so adjust: the rule
	// posts 1200.00 and the numbers below follow from it.
	trailing := [][2]string{{"2026-06-01", "2026-06-30"}, {"2026-07-01", "2026-07-31"}, {"2026-08-01", "2026-08-31"}}
	m, err := l.Metrics(ctx, "2026-09-01", "2026-09-30", 30, 14, "2026-09-14", trailing)
	if err != nil {
		t.Fatal(err)
	}
	if m.Expense != 124000 || m.AverageDaily != 8857 || m.RecurringDue != 0 {
		t.Fatalf("%+v", m)
	}
	if m.Projected != 8857*30 {
		t.Fatalf("projected %d", m.Projected)
	}
	if m.Trailing != 40000 {
		t.Fatalf("trailing %d", m.Trailing)
	}
	// 900000 less everything spent, 656000, over a 40000 trailing average.
	if m.RunwayMonths != 16.4 {
		t.Fatalf("runway %v", m.RunwayMonths)
	}
	if m.FixedShare != 96 {
		t.Fatalf("fixed share %d", m.FixedShare)
	}
	// A rule due before the period ends counts toward the projection.
	gym, err := l.AddRule(ctx, Rule{Name: "Gym", Template: Template{Kind: Expense, AccountID: a.ID, Amount: 2990, CategoryID: "Sport & fitness"}, Frequency: "monthly", StartDate: "2026-09-20"})
	if err != nil {
		t.Fatal(err)
	}
	_ = gym
	m, _ = l.Metrics(ctx, "2026-09-01", "2026-09-30", 30, 14, "2026-09-14", trailing)
	if m.RecurringDue != 2990 || m.Projected != 8857*30+2990 {
		t.Fatalf("%+v", m)
	}
}

func TestJournalAndCSV(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "House Bank", Checking, "EUR", 100000)
	card := mustAccount(t, l, "Card", CreditCard, "EUR", 0)
	usd := mustAccount(t, l, "Dollars", Checking, "USD", 50000)
	coffee := mustCategory(t, l, "Coffee & snacks")
	groceries := mustCategory(t, l, "Groceries")
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: main.ID, Amount: 450, CategoryID: coffee.ID, Date: "2026-09-02", Description: "Coffee", Tags: []string{"work"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: card.ID, Amount: 5000, Date: "2026-09-03", Notes: "two shops",
		Splits: []Split{{CategoryID: groceries.ID, Amount: 3000}, {CategoryID: coffee.ID, Amount: 2000}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Add(ctx, Transaction{Kind: Transfer, AccountID: main.ID, CounterAccountID: card.ID, Amount: 5000, Date: "2026-09-04"}); err != nil {
		t.Fatal(err)
	}
	received := int64(11000)
	if _, err := l.Add(ctx, Transaction{Kind: Transfer, AccountID: main.ID, CounterAccountID: usd.ID, Amount: 10000, CounterAmount: &received, Date: "2026-09-05", Description: "Dollars"}); err != nil {
		t.Fatal(err)
	}

	j, err := l.Journal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"2026-01-01 * Opening balance\n    assets:house-bank    1000.00 EUR\n    equity:opening balances\n",
		"2026-09-02 * Coffee\n    ; tags: work\n    expenses:food:coffee-snacks    4.50 EUR\n    assets:house-bank    -4.50 EUR\n",
		"    ; two shops\n    expenses:food:groceries    30.00 EUR\n    expenses:food:coffee-snacks    20.00 EUR\n    liabilities:card    -50.00 EUR\n",
		"2026-09-04 * Transfer\n    liabilities:card    50.00 EUR\n    assets:house-bank    -50.00 EUR\n",
		"2026-09-05 * Dollars\n    assets:dollars    110.00 USD @@ 100.00 EUR\n    assets:house-bank    -100.00 EUR\n",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("journal lacks:\n%s\ngot:\n%s", want, j)
		}
	}
	c, err := l.CSV(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(c), "\n")
	if len(lines) != 5 || !strings.HasPrefix(lines[0], "id,date,kind,amount") || !strings.Contains(lines[1], "2026-09-02,expense,-4.50,EUR,-4.50,House Bank,,Coffee & snacks,Coffee,,cleared,work") {
		t.Fatalf("csv:\n%s", c)
	}
}
