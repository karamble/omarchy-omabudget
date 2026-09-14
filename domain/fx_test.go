package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/money"
)

func TestSetRate(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()

	r, err := l.SetRate(ctx, "usd", "0.92", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Currency != "USD" || r.Date != "2026-09-13" || r.Rate != "0.92" {
		t.Fatalf("%+v", r)
	}
	// The same day is replaced, not doubled.
	if _, err := l.SetRate(ctx, "USD", "0.93", "2026-09-13"); err != nil {
		t.Fatal(err)
	}
	list, err := l.Rates(ctx, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Rate != "0.93" {
		t.Fatalf("%+v", list)
	}

	for _, bad := range []struct {
		name, currency, rate, date string
	}{
		{"the base currency", "EUR", "1", ""},
		{"a two-letter code", "US", "0.9", ""},
		{"a rate that is not a number", "USD", "nine", ""},
		{"a rate of zero", "USD", "0", ""},
		{"a negative rate", "USD", "-0.9", ""},
		{"a date that is not a date", "USD", "0.9", "October"},
	} {
		if _, err := l.SetRate(ctx, bad.currency, money.Rate(bad.rate), bad.date); err == nil {
			t.Errorf("%s was accepted", bad.name)
		}
	}

	// Newest first, across currencies.
	if _, err := l.SetRate(ctx, "USD", "0.95", "2026-10-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetRate(ctx, "GBP", "1.18", "2026-09-20"); err != nil {
		t.Fatal(err)
	}
	all, _ := l.Rates(ctx, "")
	if len(all) != 3 || all[0].Date != "2026-10-01" || all[2].Date != "2026-09-13" {
		t.Fatalf("%+v", all)
	}

	if err := l.RemoveRate(ctx, "USD", "2026-10-01"); err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveRate(ctx, "USD", "2026-10-01"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestRateOn(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	if _, err := l.SetRate(ctx, "USD", "0.90", "2026-06-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetRate(ctx, "USD", "0.95", "2026-09-01"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		date, want string
	}{
		{"2026-09-10", "0.95"},
		{"2026-09-01", "0.95"},
		{"2026-08-31", "0.90"},
		{"2026-06-01", "0.90"},
		{"2026-01-01", "0.90"}, // before any rate: the earliest beats nothing
	}
	for _, c := range cases {
		got, ok, err := l.RateOn(ctx, "USD", c.date)
		if err != nil || !ok {
			t.Fatalf("%s: %v %v", c.date, ok, err)
		}
		if string(got) != c.want {
			t.Errorf("%s: got %s want %s", c.date, got, c.want)
		}
	}
	// The base currency is always one, and an unknown currency has nothing.
	if r, ok, _ := l.RateOn(ctx, "EUR", "2026-09-10"); !ok || r != "1" {
		t.Fatalf("base rate %v %v", r, ok)
	}
	if _, ok, _ := l.RateOn(ctx, "JPY", "2026-09-10"); ok {
		t.Fatal("a currency with no rate reported one")
	}
}

// TestForeignBalancesAreCounted is the gap this closes: an account in another
// currency used to be skipped by every headline figure.
func TestForeignBalancesAreCounted(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	mustAccount(t, l, "Main", Checking, "EUR", 100000)
	mustAccount(t, l, "Dollars", Checking, "USD", 50000)

	// With no rate on file the dollars are left out, and said to be.
	liquid, missing, err := l.Liquid(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if liquid != 100000 || len(missing) != 1 || missing[0] != "USD" {
		t.Fatalf("liquid %d missing %v", liquid, missing)
	}

	if _, err := l.SetRate(ctx, "USD", "0.90", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	liquid, missing, err = l.Liquid(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if liquid != 100000+45000 || len(missing) != 0 {
		t.Fatalf("liquid %d missing %v", liquid, missing)
	}

	// The cash series counts it too, at the same rate.
	series, err := l.LiquidSeries(ctx, "2026-09-01", "2026-09-13")
	if err != nil {
		t.Fatal(err)
	}
	last := series.Series[len(series.Series)-1]
	if last.Liquid != 145000 {
		t.Fatalf("series ends at %d", last.Liquid)
	}
}

// TestStatisticsExclusion pins the two ways a category flagged out of the
// statistics used to slip through.
func TestStatisticsExclusion(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 1000000)
	// Savings & Investments is seeded flagged, and so are its children.
	emergency := mustCategory(t, l, "Emergency fund")
	groceries := mustCategory(t, l, "Groceries")

	spend(t, l, a, groceries.ID, "2026-09-02", 5000)
	spend(t, l, a, emergency.ID, "2026-09-03", 20000)
	// A split with one line in a flagged category and one outside it.
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 30000, Date: "2026-09-04",
		Splits: []Split{{CategoryID: groceries.ID, Amount: 10000}, {CategoryID: emergency.ID, Amount: 20000}}}); err != nil {
		t.Fatal(err)
	}

	tot, err := l.Totals(ctx, "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	// 50 of groceries, plus the split's 100 of groceries. The 200 and 200 in
	// the flagged category count nowhere.
	if tot.Expense != 15000 {
		t.Fatalf("expense %d, want 15000", tot.Expense)
	}

	rep, err := l.Spending(ctx, "2026-09-01", "2026-09-30", "2026-08-01", "2026-08-31", "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != tot.Expense {
		t.Fatalf("the report says %d and the headline says %d", rep.Total, tot.Expense)
	}
	for _, row := range rep.Rows {
		if row.Name == "Savings & Investments" {
			t.Fatal("a flagged category is in the report")
		}
	}

	// A budget still counts it, because budgeting one is a deliberate act.
	if err := l.SetBudget(ctx, emergency.ID, "2026-09", 50000); err != nil {
		t.Fatal(err)
	}
	b, err := l.Budgets(ctx, "2026-09", "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Cards) != 1 || b.Cards[0].Spent != 40000 {
		t.Fatalf("%+v", b.Cards)
	}
}

// TestSeedRates pins the arithmetic and the two ways it must stay out of the
// way: a table with anything in it, and a base outside the reference set.
func TestSeedRates(t *testing.T) {
	ctx := context.Background()

	// Base EUR: the other two are filed at their euro values.
	l := newLedger(t)
	n, err := l.SeedRates(ctx)
	if err != nil || n != 2 {
		t.Fatalf("filed %d: %v", n, err)
	}
	want := map[string]string{"USD": "0.92", "PLN": "0.235"}
	list, err := l.Rates(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("%+v", list)
	}
	for _, r := range list {
		if r.Date != referenceDate {
			t.Errorf("%s is dated %s", r.Currency, r.Date)
		}
		if string(r.Rate) != want[r.Currency] {
			t.Errorf("%s is %s, want %s", r.Currency, r.Rate, want[r.Currency])
		}
	}

	// A second run leaves the table alone, and so does a removed rate.
	if n, err := l.SeedRates(ctx); err != nil || n != 0 {
		t.Fatalf("a second run filed %d: %v", n, err)
	}

	// Base PLN: the same reference, crossed. 1 EUR is 1/0.235 zloty.
	zl := newLedger(t)
	zl.base = "PLN"
	if _, err := zl.SeedRates(ctx); err != nil {
		t.Fatal(err)
	}
	crossed := map[string]string{}
	all, _ := zl.Rates(ctx, "")
	for _, r := range all {
		crossed[r.Currency] = string(r.Rate)
	}
	if crossed["EUR"] != "4.255319" || crossed["USD"] != "3.914894" {
		t.Fatalf("%+v", crossed)
	}

	// A base nothing is quoted against gets nothing, which beats a guess.
	yen := newLedger(t)
	yen.base = "JPY"
	if n, err := yen.SeedRates(ctx); err != nil || n != 0 {
		t.Fatalf("filed %d for an unquoted base: %v", n, err)
	}
}
