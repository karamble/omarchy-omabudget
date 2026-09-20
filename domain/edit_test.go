package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/karamble/omarchy-omabudget/db"
)

func mustCategory(t *testing.T, l *Ledger, ref string) Category {
	t.Helper()
	c, err := l.Category(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGetLoadsLinesAndTags(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 10000)
	groceries := mustCategory(t, l, "Groceries")
	fuel := mustCategory(t, l, "Fuel")
	added, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 5000, Description: "Weekend",
		Tags:   []string{"weekly", "car"},
		Splits: []Split{{CategoryID: groceries.ID, Amount: 3000}, {CategoryID: fuel.ID, Amount: 2000}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.Get(ctx, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Splits) != 2 || got.Splits[0].CategoryID != groceries.ID || got.Splits[0].Amount != -3000 || got.Splits[1].Amount != -2000 {
		t.Fatalf("splits %+v", got.Splits)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "car" || got.Tags[1] != "weekly" {
		t.Fatalf("tags %v", got.Tags)
	}
	list, err := l.Transactions(ctx, Filter{AccountID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].Splits) != 2 || len(list[0].Tags) != 2 {
		t.Fatalf("listing %+v", list)
	}
	if _, err := l.Get(ctx, "nope"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestUpdateRewritesAndKeepsCreation(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 10000)
	coffee := mustCategory(t, l, "Coffee & snacks")
	fuel := mustCategory(t, l, "Fuel")
	added, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 450, CategoryID: coffee.ID, Description: "Coffee", Tags: []string{"work"}})
	if err != nil {
		t.Fatal(err)
	}
	if b := balance(t, l, a.ID); b != 10000-450 {
		t.Fatalf("balance %d", b)
	}
	l.now = func() time.Time { return time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC) }

	edited := added
	edited.Amount = 6000 // positive on purpose: the sign follows the kind
	edited.CategoryID = fuel.ID
	edited.Description = "Fuel instead"
	edited.Tags = []string{"car"}
	out, err := l.Update(ctx, edited)
	if err != nil {
		t.Fatal(err)
	}
	if out.Amount != -6000 || out.ReferenceAmount != -6000 || out.CategoryID != fuel.ID {
		t.Fatalf("%+v", out)
	}
	if out.CreatedAt != added.CreatedAt || out.ModifiedAt == added.ModifiedAt {
		t.Fatalf("stamps created %s modified %s, was %s", out.CreatedAt, out.ModifiedAt, added.ModifiedAt)
	}
	got, err := l.Get(ctx, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount != -6000 || got.Description != "Fuel instead" || len(got.Tags) != 1 || got.Tags[0] != "car" {
		t.Fatalf("%+v", got)
	}
	if b := balance(t, l, a.ID); b != 10000-6000 {
		t.Fatalf("balance %d", b)
	}

	// Turning it into income needs an income category, and flips the sign.
	flipped := got
	flipped.Kind = Income
	if _, err := l.Update(ctx, flipped); err == nil {
		t.Fatal("an expense category on income was accepted")
	}
	flipped.CategoryID = mustCategory(t, l, "Primary salary").ID
	out, err = l.Update(ctx, flipped)
	if err != nil {
		t.Fatal(err)
	}
	if out.Amount != 6000 {
		t.Fatalf("amount %d", out.Amount)
	}
	if b := balance(t, l, a.ID); b != 10000+6000 {
		t.Fatalf("balance %d", b)
	}
}

func TestUpdateReplacesSplitsAtomically(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 10000)
	groceries := mustCategory(t, l, "Groceries")
	fuel := mustCategory(t, l, "Fuel")
	added, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 5000,
		Splits: []Split{{CategoryID: groceries.ID, Amount: 3000}, {CategoryID: fuel.ID, Amount: 2000}}})
	if err != nil {
		t.Fatal(err)
	}
	one := added
	one.Splits = []Split{{CategoryID: fuel.ID, Amount: 5000}}
	if _, err := l.Update(ctx, one); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Get(ctx, added.ID)
	if len(got.Splits) != 1 || got.Splits[0].CategoryID != fuel.ID || got.Splits[0].Amount != -5000 {
		t.Fatalf("%+v", got.Splits)
	}
	short := got
	short.Splits = []Split{{CategoryID: fuel.ID, Amount: 1000}}
	if _, err := l.Update(ctx, short); err == nil {
		t.Fatal("lines short of the total were accepted")
	}
	got, _ = l.Get(ctx, added.ID)
	if len(got.Splits) != 1 || got.Splits[0].Amount != -5000 {
		t.Fatalf("a refused edit changed the lines: %+v", got.Splits)
	}
}

func TestUpdateKeepsTheFrozenRate(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Dollars", Checking, "USD", 100000)
	coffee := mustCategory(t, l, "Coffee & snacks")
	added, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 1000, CategoryID: coffee.ID, FXRate: "0.9"})
	if err != nil {
		t.Fatal(err)
	}
	if added.ReferenceAmount != -900 {
		t.Fatalf("base %d", added.ReferenceAmount)
	}
	edited := added
	edited.Amount = 2000
	edited.FXRate = ""
	out, err := l.Update(ctx, edited)
	if err != nil {
		t.Fatal(err)
	}
	if out.FXRate != "0.9" || out.ReferenceAmount != -1800 {
		t.Fatalf("rate %s base %d", out.FXRate, out.ReferenceAmount)
	}
	edited.FXRate = "0.5"
	out, err = l.Update(ctx, edited)
	if err != nil {
		t.Fatal(err)
	}
	if out.ReferenceAmount != -1000 {
		t.Fatalf("base %d", out.ReferenceAmount)
	}
}

func TestUpdateDeletedRefused(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 10000)
	coffee := mustCategory(t, l, "Coffee & snacks")
	added, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 450, CategoryID: coffee.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SoftDelete(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Update(ctx, added); err == nil {
		t.Fatal("a deleted transaction was edited")
	}
	got, err := l.Get(ctx, added.ID)
	if err != nil || got.DeletedAt == "" {
		t.Fatalf("get after delete: %+v %v", got, err)
	}
	if err := l.Restore(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Update(ctx, added); err != nil {
		t.Fatal(err)
	}
}

func TestListingFilters(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 10000)
	coffee := mustCategory(t, l, "Coffee & snacks")
	groceries := mustCategory(t, l, "Groceries")
	salary := mustCategory(t, l, "Primary salary")
	first, _ := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 450, CategoryID: coffee.ID, Date: "2026-09-01", Description: "Coffee at the station"})
	second, _ := l.Add(ctx, Transaction{Kind: Income, AccountID: a.ID, Amount: 300000, CategoryID: salary.ID, Date: "2026-09-05", Description: "Salary"})
	third, _ := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 5000, CategoryID: groceries.ID, Date: "2026-09-10", Description: "Weekly shop", Notes: "with the kids"})

	ids := func(f Filter) []string {
		t.Helper()
		list, err := l.Transactions(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range list {
			out = append(out, x.ID)
		}
		return out
	}
	same := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	}
	same(ids(Filter{}), third.ID, second.ID, first.ID)
	same(ids(Filter{Search: "station"}), first.ID)
	same(ids(Filter{Search: "KIDS"}), third.ID)
	same(ids(Filter{Kind: Income}), second.ID)
	same(ids(Filter{Limit: 1, Offset: 1}), second.ID)
	same(ids(Filter{Status: Cleared, From: "2026-09-05"}), third.ID, second.ID)

	if err := l.SoftDelete(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	same(ids(Filter{}), third.ID, first.ID)
	same(ids(Filter{Deleted: true}), second.ID)
	gone, _ := l.Transactions(ctx, Filter{Deleted: true})
	if gone[0].DeletedAt == "" {
		t.Fatal("deleted row without a deletion time")
	}
}

func TestUpdateAccount(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 10000)
	spare := mustAccount(t, l, "Spare", Cash, "EUR", 0)
	coffee := mustCategory(t, l, "Coffee & snacks")
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 450, CategoryID: coffee.ID}); err != nil {
		t.Fatal(err)
	}

	low := int64(50000)
	a.Name, a.Institution, a.LowBalance, a.IncludeInNetWorth = "Everyday", "House bank", &low, false
	out, err := l.UpdateAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.Account(ctx, out.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Everyday" || got.Institution != "House bank" || got.LowBalance == nil || *got.LowBalance != 50000 || got.IncludeInNetWorth {
		t.Fatalf("%+v", got)
	}
	if b := balance(t, l, a.ID); b != 10000-450 {
		t.Fatalf("balance moved to %d", b)
	}

	retyped := got
	retyped.Type = Savings
	if _, err := l.UpdateAccount(ctx, retyped); err == nil {
		t.Fatal("type changed on an account with postings")
	}
	spare.Type = Prepaid
	if _, err := l.UpdateAccount(ctx, spare); err != nil {
		t.Fatalf("type change on an empty account: %v", err)
	}
	nameless := got
	nameless.Name = " "
	if _, err := l.UpdateAccount(ctx, nameless); err == nil {
		t.Fatal("empty name accepted")
	}
	unknown := got
	unknown.ID = "nope"
	if _, err := l.UpdateAccount(ctx, unknown); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	closed := got
	closed.Active = false
	if _, err := l.UpdateAccount(ctx, closed); err != nil {
		t.Fatal(err)
	}
	got, _ = l.Account(ctx, a.ID)
	if got.Active {
		t.Fatal("still active")
	}
}
