package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/karamble/omarchy-omabudget/db"
)

func TestNextDate(t *testing.T) {
	cases := []struct {
		freq, dayRule, start, from, want string
		interval                         int
	}{
		{"daily", "", "2026-01-01", "2026-01-31", "2026-02-01", 0},
		{"weekly", "", "2026-01-01", "2026-01-01", "2026-01-08", 0},
		{"biweekly", "", "2026-01-01", "2026-12-25", "2027-01-08", 0},
		{"custom", "", "2026-01-01", "2026-01-01", "2026-01-11", 10},
		{"monthly", "", "2026-01-31", "2026-01-31", "2026-02-28", 0},
		{"monthly", "", "2026-01-31", "2026-02-28", "2026-03-31", 0},
		{"monthly", "", "2026-03-03", "2026-12-03", "2027-01-03", 0},
		{"monthly", "last", "2026-01-15", "2026-01-31", "2026-02-28", 0},
		{"monthly", "last", "2026-01-15", "2026-02-28", "2026-03-31", 0},
		{"quarterly", "", "2026-11-30", "2026-11-30", "2027-02-28", 0},
		{"semiannual", "", "2026-08-31", "2026-08-31", "2027-02-28", 0},
		{"annual", "", "2024-02-29", "2024-02-29", "2025-02-28", 0},
		{"annual", "", "2024-02-29", "2027-02-28", "2028-02-29", 0},
	}
	for _, c := range cases {
		r := Rule{Frequency: c.freq, DayRule: c.dayRule, StartDate: c.start, IntervalDays: c.interval}
		got, err := nextDate(c.from, r)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if got != c.want {
			t.Errorf("%s %q from %s: got %s want %s", c.freq, c.dayRule, c.from, got, c.want)
		}
	}
}

func rent(t *testing.T, l *Ledger, a Account, start string) Rule {
	t.Helper()
	r, err := l.AddRule(context.Background(), Rule{
		Name:      "Rent",
		Template:  Template{Kind: Expense, AccountID: a.ID, Amount: 120000, CategoryID: "Rent", Description: "Rent"},
		Frequency: "monthly", StartDate: start, LeadDays: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRuleValidation(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 500000)
	base := Rule{Name: "Rent", Template: Template{Kind: Expense, AccountID: a.ID, Amount: 120000, CategoryID: "Rent"}, Frequency: "monthly", StartDate: "2026-10-01"}
	bad := []struct {
		name string
		edit func(r *Rule)
	}{
		{"no name", func(r *Rule) { r.Name = " " }},
		{"unknown frequency", func(r *Rule) { r.Frequency = "fortnightly" }},
		{"custom without interval", func(r *Rule) { r.Frequency = "custom" }},
		{"bad day rule", func(r *Rule) { r.DayRule = "first" }},
		{"bad start", func(r *Rule) { r.StartDate = "October" }},
		{"end before start", func(r *Rule) { r.EndDate = "2026-09-01" }},
		{"zero amount", func(r *Rule) { r.Template.Amount = 0 }},
		{"income category on an expense", func(r *Rule) { r.Template.CategoryID = "Primary salary" }},
		{"unknown account", func(r *Rule) { r.Template.AccountID = "nope" }},
		{"transfer without destination", func(r *Rule) { r.Template.Kind = Transfer; r.Template.CategoryID = "" }},
	}
	for _, c := range bad {
		r := base
		c.edit(&r)
		if _, err := l.AddRule(ctx, r); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	good, err := l.AddRule(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if good.Template.CategoryID != "housing/rent" || good.Template.Currency != "EUR" || good.NextDue != "2026-10-01" || !good.Active {
		t.Fatalf("%+v", good)
	}
	if _, err := l.Rule(ctx, "nope"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestPostAndSkip(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 500000)
	r := rent(t, l, a, "2026-09-03")

	tx, err := l.Post(ctx, r.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Amount != -120000 || tx.Date != "2026-09-03" || tx.CategoryID != "housing/rent" || tx.RecurringRuleID != r.ID || !tx.IsRecurringInstance || tx.Description != "Rent" {
		t.Fatalf("%+v", tx)
	}
	if b := balance(t, l, a.ID); b != 500000-120000 {
		t.Fatalf("balance %d", b)
	}
	got, _ := l.Rule(ctx, r.ID)
	if got.NextDue != "2026-10-03" || got.LastPosted != "2026-09-03" || got.Posted != 1 {
		t.Fatalf("%+v", got)
	}
	// The instance is a normal transaction, listed with its rule.
	list, _ := l.Transactions(ctx, Filter{})
	if len(list) != 1 || list[0].RecurringRuleID != r.ID {
		t.Fatalf("%+v", list)
	}

	// A variable amount and a different date, posted on the day it was paid.
	tx, err = l.Post(ctx, r.ID, "2026-10-05", 125000)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Amount != -125000 || tx.Date != "2026-10-05" {
		t.Fatalf("%+v", tx)
	}
	got, _ = l.Rule(ctx, r.ID)
	if got.NextDue != "2026-11-03" || got.Posted != 2 {
		t.Fatalf("%+v", got)
	}

	skipped, err := l.Skip(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if skipped.NextDue != "2026-12-03" || skipped.Posted != 2 {
		t.Fatalf("%+v", skipped)
	}
	if b := balance(t, l, a.ID); b != 500000-120000-125000 {
		t.Fatalf("balance %d", b)
	}
}

func TestRuleEnds(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 500000)
	counted, err := l.AddRule(ctx, Rule{Name: "Instalment", Template: Template{Kind: Expense, AccountID: a.ID, Amount: 10000, CategoryID: "Loan principal repayment"},
		Frequency: "monthly", StartDate: "2026-09-01", OccurrenceCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Post(ctx, counted.ID, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Post(ctx, counted.ID, "", 0); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Rule(ctx, counted.ID)
	if got.Active {
		t.Fatalf("still active after two of two: %+v", got)
	}
	if _, err := l.Post(ctx, counted.ID, "", 0); err == nil {
		t.Fatal("posted a finished rule")
	}

	dated, err := l.AddRule(ctx, Rule{Name: "Trial", Template: Template{Kind: Expense, AccountID: a.ID, Amount: 999, CategoryID: "Streaming"},
		Frequency: "weekly", StartDate: "2026-09-01", EndDate: "2026-09-10"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Skip(ctx, dated.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = l.Rule(ctx, dated.ID)
	if got.NextDue != "2026-09-08" || !got.Active {
		t.Fatalf("%+v", got)
	}
	if _, err := l.Skip(ctx, dated.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = l.Rule(ctx, dated.ID)
	if got.Active {
		t.Fatalf("active past its end: %+v", got)
	}
	rules, _ := l.Rules(ctx, false)
	if len(rules) != 0 {
		t.Fatalf("finished rules listed as active: %+v", rules)
	}
	all, _ := l.Rules(ctx, true)
	if len(all) != 2 {
		t.Fatalf("%d rules", len(all))
	}
}

func TestUpcomingAndAutoPost(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 500000)
	rent(t, l, a, "2026-09-03") // overdue by the 13th
	if _, err := l.AddRule(ctx, Rule{Name: "Gym", Template: Template{Kind: Expense, AccountID: a.ID, Amount: 2990, CategoryID: "Sport & fitness"},
		Frequency: "weekly", StartDate: "2026-09-15", AutoPost: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AddRule(ctx, Rule{Name: "Salary", Template: Template{Kind: Income, AccountID: a.ID, Amount: 325000, CategoryID: "Primary salary"},
		Frequency: "monthly", StartDate: "2026-10-01", AutoPost: true}); err != nil {
		t.Fatal(err)
	}

	due, err := l.Upcoming(ctx, "2026-09-13", "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range due {
		names = append(names, d.Name+"@"+d.Date)
	}
	want := []string{"Rent@2026-09-03", "Gym@2026-09-15", "Gym@2026-09-22", "Gym@2026-09-29", "Salary@2026-10-01", "Rent@2026-10-03", "Gym@2026-10-06", "Gym@2026-10-13"}
	if len(names) != len(want) {
		t.Fatalf("got %v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v want %v", names, want)
		}
	}
	if !due[0].Overdue || due[0].DaysUntil != -10 || due[1].Overdue || due[1].DaysUntil != 2 {
		t.Fatalf("%+v %+v", due[0], due[1])
	}

	// Only the auto-posting rules post, and they catch up every missed date.
	n, err := l.PostDue(ctx, "2026-10-02")
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 { // gym on the 15th, 22nd, 29th, salary on the 1st
		t.Fatalf("posted %d", n)
	}
	list, _ := l.Transactions(ctx, Filter{})
	if len(list) != 4 {
		t.Fatalf("%d instances", len(list))
	}
	if b := balance(t, l, a.ID); b != 500000-3*2990+325000 {
		t.Fatalf("balance %d", b)
	}
	n, _ = l.PostDue(ctx, "2026-10-02")
	if n != 0 {
		t.Fatalf("posted again: %d", n)
	}
}

func TestUpdateAndRemoveRule(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 500000)
	r := rent(t, l, a, "2026-09-03")
	if _, err := l.Post(ctx, r.ID, "", 0); err != nil {
		t.Fatal(err)
	}
	edited, _ := l.Rule(ctx, r.ID)
	edited.Name = "Rent, new flat"
	edited.Template.Amount = 135000
	edited.AutoPost = true
	out, err := l.UpdateRule(ctx, edited)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "Rent, new flat" || out.Template.Amount != 135000 || !out.AutoPost || out.NextDue != "2026-10-03" || out.Posted != 1 {
		t.Fatalf("%+v", out)
	}
	edited.StartDate = "2026-11-15"
	edited.NextDue = ""
	out, err = l.UpdateRule(ctx, edited)
	if err != nil {
		t.Fatal(err)
	}
	if out.NextDue != "2026-11-15" {
		t.Fatalf("next due %s", out.NextDue)
	}
	if err := l.RemoveRule(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Rule(ctx, r.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := l.RemoveRule(ctx, r.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
	list, _ := l.Transactions(ctx, Filter{})
	if len(list) != 1 {
		t.Fatal("the posted instance went with the rule")
	}
}
