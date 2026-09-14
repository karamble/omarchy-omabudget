package domain

import (
	"context"
	"strings"
	"testing"
)

// TestReconcile walks one statement: some lines on it, one not, the sheet out
// until the odd one is put back, and settled lines that stay settled.
func TestReconcile(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	groceries := mustCategory(t, l, "Groceries")

	first := spendAt(t, l, a, groceries.ID, "2026-09-02", 5000)
	second := spendAt(t, l, a, groceries.ID, "2026-09-05", 3000)
	// After the statement date, so it is none of this sheet's business.
	spendAt(t, l, a, groceries.ID, "2026-09-20", 9900)

	// The bank says 920.00: the opening 1000 less the two September lines.
	r, err := l.Reconcile(ctx, "Main", "2026-09-10", 92000)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 2 || r.Rows[0].ID != first.ID {
		t.Fatalf("rows %+v, want the two in the window oldest first", r.Rows)
	}
	if r.Settled != 100000 || r.Ticked != -8000 || !r.Balanced() {
		t.Fatalf("settled %d ticked %d difference %d", r.Settled, r.Ticked, r.Difference)
	}

	// Take one off the statement and the sheet goes out by its amount.
	if _, err := l.SetStatus(ctx, second.ID, Pending); err != nil {
		t.Fatal(err)
	}
	r, err = l.Reconcile(ctx, "Main", "2026-09-10", 92000)
	if err != nil {
		t.Fatal(err)
	}
	if r.Difference != -3000 {
		t.Fatalf("difference %d, want -3000", r.Difference)
	}
	if _, _, err := l.FinishReconcile(ctx, "Main", "2026-09-10", 92000); err == nil ||
		!strings.Contains(err.Error(), "out by") {
		t.Fatalf("a sheet that does not add up was settled: %v", err)
	}

	// A statement that leaves it pending is the other honest answer.
	r, n, err := l.FinishReconcile(ctx, "Main", "2026-09-10", 95000)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || r.Settled != 95000 || r.Ticked != 0 {
		t.Fatalf("settled %d lines, sheet %+v", n, r)
	}

	// The settled line is closed; the one left pending comes back next time.
	again, err := l.Reconcile(ctx, "Main", "2026-09-10", 95000)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Rows) != 1 || again.Rows[0].ID != second.ID {
		t.Fatalf("rows %+v", again.Rows)
	}
	if again.Settled != 95000 || again.Difference != 0 {
		t.Fatalf("%+v", again)
	}
}

// TestReconcileCountsBothLegs pins that a transfer settles from either side.
func TestReconcileCountsBothLegs(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	from := mustAccount(t, l, "Checking", Checking, "EUR", 100000)
	to := mustAccount(t, l, "Savings", Savings, "EUR", 0)
	if _, err := l.Add(ctx, Transaction{Kind: Transfer, AccountID: from.ID, CounterAccountID: to.ID,
		Amount: 25000, Date: "2026-09-03"}); err != nil {
		t.Fatal(err)
	}

	out, err := l.Reconcile(ctx, "Checking", "2026-09-10", 75000)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Balanced() || len(out.Rows) != 1 || out.Rows[0].Signed != -25000 {
		t.Fatalf("%+v", out)
	}
	// The same row, the other way round, on the account that received it.
	in, err := l.Reconcile(ctx, "Savings", "2026-09-10", 25000)
	if err != nil {
		t.Fatal(err)
	}
	if !in.Balanced() || len(in.Rows) != 1 || in.Rows[0].Signed != 25000 {
		t.Fatalf("%+v", in)
	}
}

// TestSetStatus refuses a status that is not one of the three.
func TestSetStatus(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	groceries := mustCategory(t, l, "Groceries")
	tx := spendAt(t, l, a, groceries.ID, "2026-09-02", 5000)

	if _, err := l.SetStatus(ctx, tx.ID, "settled"); err == nil {
		t.Fatal("a status outside the three was accepted")
	}
	got, err := l.SetStatus(ctx, tx.ID, Pending)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Pending {
		t.Fatalf("status %s", got.Status)
	}
}

// spendAt is spend that hands back the transaction it made.
func spendAt(t *testing.T, l *Ledger, a Account, categoryID, date string, minor int64) Transaction {
	t.Helper()
	tx, err := l.Add(context.Background(), Transaction{Kind: Expense, AccountID: a.ID,
		CategoryID: categoryID, Amount: minor, Date: date})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
