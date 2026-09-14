package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/karamble/omarchy-omabudget/db"
)

func TestPayeeFromATransaction(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 500000)
	coffee := mustCategory(t, l, "Coffee & snacks")

	// Naming one that does not exist makes it, the way a tag is made.
	first, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 450,
		CategoryID: coffee.ID, PayeeName: "The Corner Cafe", Date: "2026-09-02"})
	if err != nil {
		t.Fatal(err)
	}
	if first.PayeeID == "" {
		t.Fatal("no payee was made")
	}
	list, err := l.Payees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "The Corner Cafe" || list[0].Uses != 1 {
		t.Fatalf("%+v", list)
	}

	// The same name again lands on the same payee, whatever the case.
	second, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 380,
		CategoryID: coffee.ID, PayeeName: "the corner cafe", Date: "2026-09-03"})
	if err != nil {
		t.Fatal(err)
	}
	if second.PayeeID != first.PayeeID {
		t.Fatal("the same name made a second payee")
	}
	if list, _ = l.Payees(ctx); len(list) != 1 || list[0].Uses != 2 {
		t.Fatalf("%+v", list)
	}

	// It comes back on the listing, and filters it.
	rows, err := l.Transactions(ctx, Filter{PayeeID: first.PayeeID})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].PayeeName != "The Corner Cafe" {
		t.Fatalf("%+v", rows)
	}
	// And the free text search reaches it.
	rows, _ = l.Transactions(ctx, Filter{Search: "corner"})
	if len(rows) != 2 {
		t.Fatalf("search found %d", len(rows))
	}
}

func TestPayeeAliasesAndMerge(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 500000)
	groceries := mustCategory(t, l, "Groceries")

	mk := func(name, date string) Transaction {
		t.Helper()
		tx, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 2000,
			CategoryID: groceries.ID, PayeeName: name, Date: date})
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	mk("Whole Foods", "2026-09-02")
	mk("WHOLEFOODS MKT 123", "2026-09-03")

	all, _ := l.Payees(ctx)
	if len(all) != 2 {
		t.Fatalf("%+v", all)
	}

	// An alias makes the statement's spelling land on the right payee.
	if _, err := l.AddAlias(ctx, "Whole Foods", "WFM"); err != nil {
		t.Fatal(err)
	}
	if p, err := l.Payee(ctx, "wfm"); err != nil || p.Name != "Whole Foods" {
		t.Fatalf("%+v %v", p, err)
	}
	byAlias := mk("WFM", "2026-09-04")
	whole, _ := l.Payee(ctx, "Whole Foods")
	if byAlias.PayeeID != whole.ID {
		t.Fatal("an alias made a new payee")
	}
	if _, err := l.AddAlias(ctx, "Whole Foods", "WHOLEFOODS MKT 123"); err == nil {
		t.Fatal("an alias that belongs to another payee was taken")
	}

	// Merging moves the transactions and keeps the old name as an alias.
	moved, err := l.MergePayees(ctx, "WHOLEFOODS MKT 123", "Whole Foods")
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("moved %d", moved)
	}
	all, _ = l.Payees(ctx)
	if len(all) != 1 || all[0].Uses != 3 {
		t.Fatalf("%+v", all)
	}
	if p, err := l.Payee(ctx, "WHOLEFOODS MKT 123"); err != nil || p.ID != whole.ID {
		t.Fatalf("the old name does not resolve: %+v %v", p, err)
	}

	// Renaming, a default category, and the refusals.
	whole, _ = l.Payee(ctx, whole.ID)
	whole.Name = "Whole Foods Market"
	whole.Category = "Groceries"
	out, err := l.UpdatePayee(ctx, whole)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "Whole Foods Market" || out.Category != groceries.ID {
		t.Fatalf("%+v", out)
	}
	if err := l.RemovePayee(ctx, out.ID); err == nil {
		t.Fatal("a payee with transactions was removed")
	}
	spare, err := l.AddAlias(ctx, out.ID, "WFM2")
	if err != nil {
		t.Fatal(err)
	}
	if len(spare.Aliases) != 3 {
		t.Fatalf("aliases %v", spare.Aliases)
	}
	if _, err := l.RemoveAlias(ctx, out.ID, "WFM2"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.RemoveAlias(ctx, out.ID, "WFM2"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err := l.Payee(ctx, "nobody"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestFilters(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 1000000)
	groceries := mustCategory(t, l, "Groceries")
	coffee := mustCategory(t, l, "Coffee & snacks")
	fuel := mustCategory(t, l, "Fuel")

	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 5000, CategoryID: groceries.ID,
		Date: "2026-09-02", Tags: []string{"weekly", "home"}, PayeeName: "Market"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 450, CategoryID: coffee.ID,
		Date: "2026-09-03", Tags: []string{"work"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 7000, CategoryID: fuel.ID,
		Date: "2026-09-04", Tags: []string{"work", "car"}}); err != nil {
		t.Fatal(err)
	}
	// A split into a category under Food, to prove a group reaches its lines.
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 3000, Date: "2026-09-05",
		Splits: []Split{{CategoryID: coffee.ID, Amount: 1000}, {CategoryID: fuel.ID, Amount: 2000}}}); err != nil {
		t.Fatal(err)
	}

	count := func(f Filter) int {
		t.Helper()
		rows, err := l.Transactions(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	if n := count(Filter{Tags: []string{"work"}}); n != 2 {
		t.Fatalf("tag work: %d", n)
	}
	if n := count(Filter{Tags: []string{"work", "home"}}); n != 3 {
		t.Fatalf("either tag: %d", n)
	}
	if n := count(Filter{Tags: []string{"WORK"}}); n != 2 {
		t.Fatalf("tags are matched whatever the case: %d", n)
	}
	if n := count(Filter{Min: 5000}); n != 2 {
		t.Fatalf("at least 50: %d", n)
	}
	if n := count(Filter{Max: 5000}); n != 3 {
		t.Fatalf("at most 50: %d", n)
	}
	if n := count(Filter{Min: 1000, Max: 6000}); n != 2 {
		t.Fatalf("between: %d", n)
	}
	// The group stands for its children, including a split's lines.
	if n := count(Filter{CategoryID: "food"}); n != 3 {
		t.Fatalf("the Food group: %d", n)
	}
	if n := count(Filter{CategoryID: coffee.ID}); n != 2 {
		t.Fatalf("one child: %d", n)
	}
	p, _ := l.Payee(ctx, "Market")
	if n := count(Filter{PayeeID: p.ID}); n != 1 {
		t.Fatalf("payee: %d", n)
	}
}
