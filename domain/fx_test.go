package domain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/feed"
	"github.com/karamble/omarchy-omabudget/money"
)

func TestSetRate(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()

	r, err := l.SetRate(ctx, "usd", "0.92", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Currency != "USD" || r.Date != "2026-09-13" || r.Rate != "0.92" || r.Source != SourceManual {
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
	if len(list) != 1 || list[0].Rate != "0.93" || list[0].Source != SourceManual {
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
	if err := l.SetBudget(ctx, emergency.ID, "2026-09", 50000, ""); err != nil {
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

// TestSeedOnFirstUse: a currency gets the shipped rate the first time an
// account is opened in it, dated the day the quote was published and marked
// as seed, and then counts in every figure. A second account in it files
// nothing more.
func TestSeedOnFirstUse(t *testing.T) {
	l := seeded(newLedger(t))
	ctx := context.Background()
	mustAccount(t, l, "Main", Checking, "EUR", 100000)
	mustAccount(t, l, "Dollars", Checking, "USD", 50000)
	list, err := l.Rates(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Currency != "USD" || list[0].Date != feed.Builtin.Date ||
		list[0].Rate != "0.87260035" || list[0].Source != SourceSeed {
		t.Fatalf("%+v", list)
	}
	if v, err := l.DB().Meta(ctx, db.MetaRateSeeded("USD")); err != nil || v != feed.Builtin.Date {
		t.Errorf("marker = %q, %v", v, err)
	}
	// 500.00 USD at 0.87260035 is 436.30 EUR.
	liquid, missing, err := l.Liquid(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if liquid != 143630 || len(missing) != 0 {
		t.Errorf("liquid %d missing %v", liquid, missing)
	}
	mustAccount(t, l, "More dollars", Savings, "USD", 0)
	if list, _ := l.Rates(ctx, "USD"); len(list) != 1 {
		t.Errorf("a second account filed again: %+v", list)
	}
	// A typed rate on the same day takes the row over.
	if _, err := l.SetRate(ctx, "USD", "0.9", feed.Builtin.Date); err != nil {
		t.Fatal(err)
	}
	if list, _ := l.Rates(ctx, "USD"); len(list) != 1 || list[0].Source != SourceManual || list[0].Rate != "0.9" {
		t.Errorf("%+v", list)
	}
}

// TestSeedAtFirstEntry: a currency that first appears on a transaction, a
// plan or an account edit is seeded there, so the entry goes through at the
// shipped rate instead of being refused.
func TestSeedAtFirstEntry(t *testing.T) {
	l := seeded(newLedger(t))
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	// 100.00 PLN at 1/4.3635, which is 0.22917383, is 22.92 EUR.
	got, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: main.ID, Amount: 10000, Currency: "PLN", CategoryID: "Fuel"})
	if err != nil {
		t.Fatal(err)
	}
	if got.FXRate != "" || got.ReferenceAmount != -2292 {
		t.Errorf("%+v", got)
	}
	if list, _ := l.Rates(ctx, "PLN"); len(list) != 1 || list[0].Source != SourceSeed {
		t.Errorf("%+v", list)
	}

	if err := l.SetBudget(ctx, "Groceries", "2026-09", 10000, "GBP"); err != nil {
		t.Fatal(err)
	}
	if list, _ := l.Rates(ctx, "GBP"); len(list) != 1 || list[0].Rate != "1.1644155" {
		t.Errorf("%+v", list)
	}

	spare := mustAccount(t, l, "Spare", Savings, "EUR", 0)
	spare.Currency = "CHF"
	if _, err := l.UpdateAccount(ctx, spare); err != nil {
		t.Fatal(err)
	}
	if list, _ := l.Rates(ctx, "CHF"); len(list) != 1 || list[0].Source != SourceSeed {
		t.Errorf("%+v", list)
	}
	// A rule is a currency in use too.
	if _, err := l.AddRule(ctx, Rule{Name: "Rent", Frequency: "monthly", StartDate: "2026-09-01",
		Template: Template{Kind: Expense, AccountID: main.ID, Amount: 100000, Currency: "NOK", CategoryID: "Rent"}}); err != nil {
		t.Fatal(err)
	}
	if list, _ := l.Rates(ctx, "NOK"); len(list) != 1 {
		t.Errorf("%+v", list)
	}
}

// TestSeedIsClosedByAnyRate: a currency that has had a rate, whoever filed
// it, is never seeded, even after that rate is removed. The marker is what
// says so, and a marker on its own, as the migration leaves, closes it too.
func TestSeedIsClosedByAnyRate(t *testing.T) {
	l := seeded(newLedger(t))
	ctx := context.Background()
	if _, err := l.SetRate(ctx, "GBP", "1.2", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveRate(ctx, "GBP", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	mustAccount(t, l, "Pounds", Checking, "GBP", 0)
	if list, _ := l.Rates(ctx, "GBP"); len(list) != 0 {
		t.Errorf("the shipped rate came back after a typed one was removed: %+v", list)
	}
	if _, err := l.db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, db.MetaRateSeeded("CHF"), "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.SeedFor(ctx, "CHF"); err != nil || ok {
		t.Errorf("seeded past a marker: %v %v", ok, err)
	}
	// A rate typed first, then the currency used: the typed one stands.
	if _, err := l.SetRate(ctx, "USD", "0.9", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	mustAccount(t, l, "Dollars", Checking, "USD", 0)
	if list, _ := l.Rates(ctx, "USD"); len(list) != 1 || list[0].Rate != "0.9" {
		t.Errorf("%+v", list)
	}
}

// TestSeedOnlyWhatIsQuoted: a currency the quote does not carry gets nothing
// and no marker, so a later quote could still offer it; the reference is
// never seeded; a reference the quote does not carry seeds nothing at all;
// and another reference is crossed through the base.
func TestSeedOnlyWhatIsQuoted(t *testing.T) {
	ctx := context.Background()
	l := seeded(newLedger(t))
	mustAccount(t, l, "Coins", Cash, "BTC", 0)
	if list, _ := l.Rates(ctx, ""); len(list) != 0 {
		t.Errorf("%+v", list)
	}
	if _, err := l.DB().Meta(ctx, db.MetaRateSeeded("BTC")); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("an unquoted currency got a marker: %v", err)
	}
	if ok, err := l.SeedFor(ctx, "EUR"); err != nil || ok {
		t.Errorf("the reference was seeded: %v %v", ok, err)
	}

	yen := seeded(newLedgerWith(t, "JPY"))
	mustAccount(t, yen, "Euros", Checking, "EUR", 0)
	mustAccount(t, yen, "Dollars", Checking, "USD", 0)
	got := map[string]money.Rate{}
	all, _ := yen.Rates(ctx, "")
	for _, r := range all {
		got[r.Currency] = r.Rate
	}
	if got["EUR"] != "180.94" || got["USD"] != "157.88831" {
		t.Errorf("%v", got)
	}

	coins := seeded(newLedgerWith(t, "BTC"))
	mustAccount(t, coins, "Dollars", Checking, "USD", 0)
	if list, _ := coins.Rates(ctx, ""); len(list) != 0 {
		t.Errorf("a reference nothing is quoted against was seeded: %+v", list)
	}

	// With no quote at all nothing is filed, which is how the other tests
	// start from an empty table.
	off := newLedger(t)
	mustAccount(t, off, "Dollars", Checking, "USD", 0)
	if list, _ := off.Rates(ctx, ""); len(list) != 0 {
		t.Errorf("%+v", list)
	}
}

// TestReferenceIsFixed: the reference the rates are quoted against is read
// from the ledger and is what SetRate, RateOn and SeedFor key off; no
// setting on the ledger can move it.
func TestReferenceIsFixed(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	if l.Reference() != "EUR" {
		t.Fatalf("reference %s", l.Reference())
	}
	if _, err := l.SetRate(ctx, "EUR", "4.25", ""); err == nil {
		t.Error("a rate for the reference was accepted")
	}
	if r, ok, _ := l.RateOn(ctx, "EUR", ""); !ok || r != "1" {
		t.Errorf("reference rate = %s %v, want 1", r, ok)
	}
	if v, ok := ToReference(1234, "EUR", "EUR", nil); !ok || v != 1234 {
		t.Errorf("an amount already in the reference read the table: %d %v", v, ok)
	}
	if _, ok := ToReference(1234, "PLN", "EUR", nil); ok {
		t.Error("a currency with no rate was counted")
	}
}

// TestRateTableCoversTransactionCurrencies: a transaction can be booked in a
// currency no account is kept in, and its rate must still be on the table,
// or it would count as nothing.
func TestRateTableCoversTransactionCurrencies(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	if _, err := l.SetRate(ctx, "PLN", "0.235", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Add(ctx, Transaction{
		Kind: Expense, AccountID: main.ID, Amount: -10000, Currency: "PLN",
		CategoryID: "food/groceries", Date: "2026-09-10",
	}); err != nil {
		t.Fatal(err)
	}
	rates, err := l.RateTable(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if rates["PLN"] != "0.235" || rates["EUR"] != "1" {
		t.Errorf("table %v lacks the transaction's currency", rates)
	}
	if _, ok := rates["USD"]; ok {
		t.Errorf("table %v names a currency nothing carries", rates)
	}
}

// TestEnteredRateIsKeptOnTheRow: a row follows the table, and carries no
// rate of its own, unless it was given one, whether or not the table agrees.
func TestEnteredRateIsKeptOnTheRow(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	pln := mustAccount(t, l, "PLN card", CreditCard, "PLN", 0)
	add := func(fxRate money.Rate, account string) Transaction {
		t.Helper()
		out, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: account, Amount: 10000, CategoryID: "Fuel", FXRate: fxRate})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := add("", main.ID); got.FXRate != "" || got.ReferenceAmount != -10000 {
		t.Errorf("reference currency row: %+v", got)
	}
	if got := add("1", main.ID); got.FXRate != "" {
		t.Errorf("a rate on a reference currency row was kept: %+v", got)
	}
	if got := add("0.232", pln.ID); got.FXRate != "0.232" || got.ReferenceAmount != -2320 {
		t.Errorf("a rate with nothing on file is kept: %+v", got)
	}
	if _, err := l.SetRate(ctx, "PLN", "0.235", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	if got := add("", pln.ID); got.FXRate != "" || got.ReferenceAmount != -2350 {
		t.Errorf("table row: %+v", got)
	}
	agreeing := add("0.2350", pln.ID)
	if agreeing.FXRate != "0.2350" || agreeing.ReferenceAmount != -2350 {
		t.Errorf("a typed rate that agrees with the table was not kept: %+v", agreeing)
	}
	typed := add("0.232", pln.ID)
	if typed.FXRate != "0.232" {
		t.Errorf("a differing rate was not kept: %+v", typed)
	}
	// The rate reads back, and survives an edit that does not touch it.
	got, err := l.Get(ctx, typed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FXRate != "0.232" {
		t.Errorf("read back as %q", got.FXRate)
	}
	got.Description = "still typed"
	got.FXRate = ""
	if out, err := l.Update(ctx, got); err != nil || out.FXRate != "0.232" || out.ReferenceAmount != -2320 {
		t.Errorf("edit lost the entered rate: %+v, %v", out, err)
	}
	// An edit that types a new rate pins the row to it, table's rate or not.
	got, _ = l.Get(ctx, agreeing.ID)
	got.FXRate = "0.20"
	if out, err := l.Update(ctx, got); err != nil || out.FXRate != "0.20" || out.ReferenceAmount != -2000 {
		t.Errorf("edit with a new rate: %+v, %v", out, err)
	}
	got.FXRate = "0.235"
	if out, err := l.Update(ctx, got); err != nil || out.FXRate != "0.235" || out.ReferenceAmount != -2350 {
		t.Errorf("edit typing the table's rate: %+v, %v", out, err)
	}
}

// TestTypedRateSurvivesCorrection: a rate typed on a row is the record of
// what was charged, so a later correction of the table leaves it alone even
// when the two agreed at the time, while a row never given a rate follows.
func TestTypedRateSurvivesCorrection(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	pln := mustAccount(t, l, "PLN card", CreditCard, "PLN", 0)
	if _, err := l.SetRate(ctx, "PLN", "0.235", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	add := func(fxRate money.Rate) Transaction {
		t.Helper()
		out, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: pln.ID, Amount: 10000, CategoryID: "Fuel", Date: "2026-02-01", FXRate: fxRate})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	typed, following := add("0.235"), add("")
	if typed.ReferenceAmount != -2350 || following.ReferenceAmount != -2350 {
		t.Fatalf("at entry: typed %d following %d", typed.ReferenceAmount, following.ReferenceAmount)
	}
	if _, err := l.SetRate(ctx, "PLN", "0.240", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.Get(ctx, typed.ID); got.FXRate != "0.235" || got.ReferenceAmount != -2350 {
		t.Errorf("the typed rate was corrected away: rate %q reference %d", got.FXRate, got.ReferenceAmount)
	}
	if got, _ := l.Get(ctx, following.ID); got.FXRate != "" || got.ReferenceAmount != -2400 {
		t.Errorf("the following row did not follow: rate %q reference %d", got.FXRate, got.ReferenceAmount)
	}
}

// TestDateEditFollowsTheTable: an edit that moves a row following the table
// to another date or currency re-derives it at the table's rate for where it
// lands, and it goes on following the table rather than being pinned at the
// old rate. A row with a rate of its own keeps it through the same edits.
func TestDateEditFollowsTheTable(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	wallet := mustAccount(t, l, "Wallet", Cash, "PLN", 0)
	for _, r := range []struct{ currency, rate, date string }{
		{"PLN", "0.235", "2026-03-01"}, {"PLN", "0.240", "2026-06-01"}, {"USD", "0.9", "2026-01-01"},
	} {
		if _, err := l.SetRate(ctx, r.currency, money.Rate(r.rate), r.date); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: wallet.ID, Amount: 10000, CategoryID: "Fuel", Date: "2026-03-10"})
	if err != nil {
		t.Fatal(err)
	}
	if got.FXRate != "" || got.ReferenceAmount != -2350 {
		t.Fatalf("on entry: %+v", got)
	}

	// The date moves under the second rate.
	got.Date = "2026-06-15"
	moved, err := l.Update(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if moved.FXRate != "" || moved.ReferenceAmount != -2400 {
		t.Errorf("after the date edit: rate %q reference %d, want following at -2400", moved.FXRate, moved.ReferenceAmount)
	}
	if _, err := l.SetRate(ctx, "PLN", "0.250", "2026-06-01"); err != nil {
		t.Fatal(err)
	}
	if again, _ := l.Get(ctx, moved.ID); again.ReferenceAmount != -2500 {
		t.Errorf("the moved row stopped following the table: %+v", again)
	}

	// The currency changes to one with its own rate on file.
	moved.Currency = "USD"
	swapped, err := l.Update(ctx, moved)
	if err != nil {
		t.Fatal(err)
	}
	if swapped.FXRate != "" || swapped.ReferenceAmount != -9000 {
		t.Errorf("after the currency edit: rate %q reference %d, want following at -9000", swapped.FXRate, swapped.ReferenceAmount)
	}
	// And to one with no rate at all, which is refused rather than pinned.
	swapped.Currency = "JPY"
	if _, err := l.Update(ctx, swapped); err == nil {
		t.Error("an edit into a currency with no rate was accepted")
	}

	// A row with its own rate keeps it through the same edits.
	pinned, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: wallet.ID, Amount: 10000, CategoryID: "Fuel", Date: "2026-03-10", FXRate: "0.2"})
	if err != nil {
		t.Fatal(err)
	}
	pinned.Date = "2026-06-15"
	pinned.FXRate = ""
	if out, err := l.Update(ctx, pinned); err != nil || out.FXRate != "0.2" || out.ReferenceAmount != -2000 {
		t.Errorf("a date edit moved an entered rate: %+v, %v", out, err)
	}
}

// TestEntryBeforeTheFirstRate: a row dated before any rate on file reads the
// earliest one, the same way a balance does, rather than being refused.
func TestEntryBeforeTheFirstRate(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	wallet := mustAccount(t, l, "Wallet", Cash, "PLN", 0)
	if _, err := l.SetRate(ctx, "PLN", "0.25", "2026-06-01"); err != nil {
		t.Fatal(err)
	}
	early, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: wallet.ID, Amount: 10000, CategoryID: "Fuel", Date: "2026-01-10"})
	if err != nil {
		t.Fatal(err)
	}
	if early.FXRate != "" || early.ReferenceAmount != -2500 {
		t.Fatalf("early row: %+v", early)
	}
	// Filing an earlier rate moves it, which is why re-derivation is not
	// windowed to dates after the changed rate.
	if out, err := l.SetRate(ctx, "PLN", "0.20", "2026-03-01"); err != nil || out.Rederived != 1 {
		t.Fatalf("rederived %d, %v", out.Rederived, err)
	}
	if got, _ := l.Get(ctx, early.ID); got.ReferenceAmount != -2000 {
		t.Errorf("the early row did not read the new earliest rate: %+v", got)
	}
}

// TestRateCorrectionRederives: filing or removing a rate rewrites the
// reference amounts of every row in that currency that follows the table,
// split lines included, each at the table's rate for its own date. Rows with
// a rate of their own stay as they were.
func TestRateCorrectionRederives(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	pln := mustAccount(t, l, "PLN card", CreditCard, "PLN", 0)
	for _, r := range []struct{ rate, date string }{{"0.23", "2026-01-01"}, {"0.25", "2026-03-01"}} {
		if _, err := l.SetRate(ctx, "PLN", money.Rate(r.rate), r.date); err != nil {
			t.Fatal(err)
		}
	}
	add := func(tx Transaction) Transaction {
		t.Helper()
		tx.Kind, tx.AccountID, tx.Amount = Expense, pln.ID, 10000
		if len(tx.Splits) == 0 {
			tx.CategoryID = "Fuel"
		}
		out, err := l.Add(ctx, tx)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	jan := add(Transaction{Date: "2026-01-15"})
	feb := add(Transaction{Date: "2026-02-10", Splits: []Split{
		{CategoryID: "food/groceries", Amount: 6000},
		{CategoryID: "food/alcohol-tobacco", Amount: 4000},
	}})
	typed := add(Transaction{Date: "2026-02-20", FXRate: "0.2320"})
	mar := add(Transaction{Date: "2026-03-10"})
	gone := add(Transaction{Date: "2026-02-25"})
	if err := l.SoftDelete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	check := func(id string, reference int64, rate money.Rate, splits ...int64) {
		t.Helper()
		got, err := l.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.ReferenceAmount != reference || got.FXRate != rate {
			t.Errorf("%s: %d at %q, want %d at %q", got.Date, got.ReferenceAmount, got.FXRate, reference, rate)
		}
		for i, want := range splits {
			if got.Splits[i].ReferenceAmount != want {
				t.Errorf("%s split %d: %d, want %d", got.Date, i, got.Splits[i].ReferenceAmount, want)
			}
		}
	}
	check(jan.ID, -2300, "")
	check(feb.ID, -2300, "", -1380, -920)
	check(typed.ID, -2320, "0.2320")
	check(mar.ID, -2500, "")
	check(gone.ID, -2300, "")

	// A rate between the two moves February only, deleted rows included, and
	// says how many rows it went over.
	out, err := l.SetRate(ctx, "PLN", "0.24", "2026-02-01")
	if err != nil {
		t.Fatal(err)
	}
	if out.Rederived != 4 {
		t.Errorf("rederived %d, want the 4 rows that follow the table", out.Rederived)
	}
	check(jan.ID, -2300, "")
	check(feb.ID, -2400, "", -1440, -960)
	check(typed.ID, -2320, "0.2320")
	check(mar.ID, -2500, "")
	check(gone.ID, -2400, "")

	// Replacing January's rate reaches January only.
	if _, err := l.SetRate(ctx, "PLN", "0.22", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	check(jan.ID, -2200, "")
	check(feb.ID, -2400, "", -1440, -960)

	// Removing February's rate hands its rows back to January's.
	if err := l.RemoveRate(ctx, "PLN", "2026-02-01"); err != nil {
		t.Fatal(err)
	}
	check(feb.ID, -2200, "", -1320, -880)
	check(gone.ID, -2200, "")
	check(typed.ID, -2320, "0.2320")

	// Removing January's leaves March's as the earliest, which every row
	// before it now reads.
	if err := l.RemoveRate(ctx, "PLN", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	check(jan.ID, -2500, "")
	check(feb.ID, -2500, "", -1500, -1000)
	check(mar.ID, -2500, "")

	// The last rate cannot go while rows follow the table.
	err = l.RemoveRate(ctx, "PLN", "2026-03-01")
	if err == nil {
		t.Fatal("the last PLN rate was removed under following rows")
	}
	if !strings.Contains(err.Error(), "PLN") {
		t.Errorf("the refusal does not name the currency: %v", err)
	}
	if r, ok, _ := l.RateOn(ctx, "PLN", "2026-03-10"); !ok || r != "0.25" {
		t.Errorf("the refused removal went through: %s %v", r, ok)
	}
	// A last rate nothing follows goes without a fuss.
	if _, err := l.SetRate(ctx, "USD", "0.9", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveRate(ctx, "USD", "2026-01-01"); err != nil {
		t.Errorf("removing an unused rate: %v", err)
	}
}

// TestRateCorrectionRefusesOverflow: a correction that would push a
// reference amount past int64 is refused whole, rate and rows alike.
func TestRateCorrectionRefusesOverflow(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	pln := mustAccount(t, l, "PLN card", CreditCard, "PLN", 0)
	if _, err := l.SetRate(ctx, "PLN", "1", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	huge, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: pln.ID, Amount: 100000000000000000, CategoryID: "Fuel", Date: "2026-02-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetRate(ctx, "PLN", "100", "2026-01-01"); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("err = %v, want ErrOverflow", err)
	}
	if r, _, _ := l.RateOn(ctx, "PLN", "2026-02-01"); r != "1" {
		t.Errorf("the refused rate was filed: %s", r)
	}
	if got, _ := l.Get(ctx, huge.ID); got.ReferenceAmount != -100000000000000000 {
		t.Errorf("the refused correction moved the row: %d", got.ReferenceAmount)
	}
}

// TestLatestRates: one row per currency, the newest, whatever else is on
// file, and none at all for a currency that has none.
func TestLatestRates(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	if got, err := l.LatestRates(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty table: %+v, %v", got, err)
	}
	for _, r := range []struct{ currency, rate, date string }{
		{"USD", "0.90", "2026-06-01"}, {"USD", "0.95", "2026-09-01"}, {"USD", "0.85", "2026-01-01"},
		{"PLN", "0.235", "2026-03-01"},
	} {
		if _, err := l.SetRate(ctx, r.currency, money.Rate(r.rate), r.date); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.LatestRates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Currency != "PLN" || got[0].Rate != "0.235" ||
		got[1].Currency != "USD" || got[1].Rate != "0.95" || got[1].Date != "2026-09-01" || got[1].Source != SourceManual {
		t.Fatalf("%+v", got)
	}
}
