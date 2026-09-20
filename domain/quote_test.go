package domain

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/feed"
	"github.com/karamble/omarchy-omabudget/money"
)

// quote is a publication of one euro in each currency on 2026-09-18, as a
// source would send it. Against EUR each rate is inverted: "0.8" files as
// 1.25, "4" as 0.25.
func quote(rates map[string]money.Rate) feed.Quote {
	return feed.Quote{Base: "EUR", Date: "2026-09-18", Rates: rates}
}

// typed files baselines by hand on one date.
func typed(t *testing.T, l *Ledger, date string, rates map[string]money.Rate) {
	t.Helper()
	for currency, rate := range rates {
		if _, err := l.SetRate(context.Background(), currency, rate, date); err != nil {
			t.Fatal(err)
		}
	}
}

func holdsOnly(t *testing.T, got Applied, want ...string) {
	t.Helper()
	var held []string
	for _, h := range got.Held {
		held = append(held, h.Currency)
	}
	if !reflect.DeepEqual(held, want) {
		t.Errorf("held %v, want %v", held, want)
	}
}

// TestApplyQuoteFilesWhatIsInUse: only the currencies in use are written,
// under the publication date and the source's name; one the source does not
// carry is reported unquoted. A second press on the same day changes
// nothing and says so, and the same rate from the other source is filed
// again under its name.
func TestApplyQuoteFilesWhatIsInUse(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	got, err := l.ApplyQuote(ctx, feed.Builtin, "ecb", []string{"usd", "PLN", "BTC", "eur", "USD"})
	if err != nil {
		t.Fatal(err)
	}
	want := Applied{Filed: []string{"PLN", "USD"}, Unchanged: []string{}, Kept: []string{}, Held: []Held{}, Unquoted: []string{"BTC"}, Notes: []string{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	all, _ := l.Rates(ctx, "")
	if len(all) != 2 || all[0].Currency != "PLN" || all[0].Rate != "0.22917383" || all[0].Source != "ecb" ||
		all[1].Currency != "USD" || all[1].Rate != "0.87260035" || all[1].Date != "2026-09-18" {
		t.Fatalf("%+v", all)
	}
	if v, err := l.DB().Meta(ctx, db.MetaRateSeeded("USD")); err != nil || v != "2026-09-18" {
		t.Errorf("a filed rate did not close seeding: %q %v", v, err)
	}

	again, err := l.ApplyQuote(ctx, feed.Builtin, "ecb", []string{"USD", "PLN"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Unchanged, []string{"PLN", "USD"}) || len(again.Filed) != 0 || again.Rederived != 0 {
		t.Errorf("second press: %+v", again)
	}

	other, err := l.ApplyQuote(ctx, feed.Builtin, "frankfurter", []string{"USD"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(other.Filed, []string{"USD"}) || len(other.Notes) != 0 {
		t.Errorf("other source: %+v", other)
	}
	if list, _ := l.Rates(ctx, "USD"); len(list) != 1 || list[0].Source != "frankfurter" {
		t.Errorf("%+v", list)
	}

	for _, bad := range []struct {
		name   string
		q      feed.Quote
		source string
	}{
		{"a source that is not a feed", feed.Builtin, SourceManual},
		{"the shipped source", feed.Builtin, SourceSeed},
		{"no source", feed.Builtin, ""},
		{"a quote with no date", feed.Quote{Base: "EUR", Rates: feed.Builtin.Rates}, "ecb"},
	} {
		if _, err := l.ApplyQuote(ctx, bad.q, bad.source, []string{"USD"}); err == nil {
			t.Errorf("%s was accepted", bad.name)
		}
	}
	coins := newLedgerWith(t, "BTC")
	if _, err := coins.ApplyQuote(ctx, feed.Builtin, "ecb", []string{"USD"}); err == nil || !strings.Contains(err.Error(), "BTC") {
		t.Errorf("an unquoted reference: %v", err)
	}
}

// TestApplyQuoteKeepsTypedRates: a rate typed for the publication date is
// never touched, by a press or by accepting a held rate.
func TestApplyQuoteKeepsTypedRates(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	typed(t, l, "2026-09-18", map[string]money.Rate{"USD": "0.9"})
	got, err := l.ApplyQuote(ctx, feed.Builtin, "ecb", []string{"USD", "PLN"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Kept, []string{"USD"}) || !reflect.DeepEqual(got.Filed, []string{"PLN"}) {
		t.Errorf("%+v", got)
	}
	if list, _ := l.Rates(ctx, "USD"); len(list) != 1 || list[0].Rate != "0.9" || list[0].Source != SourceManual {
		t.Errorf("%+v", list)
	}
	if _, err := l.AcceptRate(ctx, "USD", "0.87260035", "2026-09-18", "ecb"); err == nil || !strings.Contains(err.Error(), "typed by hand") {
		t.Errorf("accepting over a typed rate: %v", err)
	}
}

// TestApplyQuoteHoldsAStalePublication: a quote more than ten days old is
// held whole, with its date and age named, except what is kept. Ten days
// exactly is filed. Age is read from the ledger's clock.
func TestApplyQuoteHoldsAStalePublication(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	typed(t, l, "2026-09-18", map[string]money.Rate{"CHF": "1.05"})
	l.now = func() time.Time { return time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC) }
	got, err := l.ApplyQuote(ctx, feed.Builtin, "ecb", []string{"USD", "PLN", "CHF"})
	if err != nil {
		t.Fatal(err)
	}
	holdsOnly(t, got, "PLN", "USD")
	if !reflect.DeepEqual(got.Kept, []string{"CHF"}) || len(got.Filed) != 0 {
		t.Errorf("%+v", got)
	}
	if h := got.Held[0]; h.Rate != "0.22917383" || h.Date != "2026-09-18" || h.Previous != "" ||
		!strings.Contains(h.Reason, "2026-09-18") || !strings.Contains(h.Reason, "13 days") {
		t.Errorf("%+v", h)
	}
	if all, _ := l.Rates(ctx, ""); len(all) != 1 {
		t.Errorf("a held batch was written: %+v", all)
	}
	l.now = func() time.Time { return time.Date(2026, 9, 28, 23, 0, 0, 0, time.UTC) }
	if got, err := l.ApplyQuote(ctx, feed.Builtin, "ecb", []string{"USD"}); err != nil || !reflect.DeepEqual(got.Filed, []string{"USD"}) {
		t.Errorf("ten days old: %+v, %v", got, err)
	}
}

// TestApplyQuoteHoldsABatchThatMovedTogether: with two or more currencies
// compared against a recent baseline, more than half moving past a quarter
// holds every candidate, the ones that did not move included, and writes
// nothing. Half or fewer is the market: the movers are filed and noted.
func TestApplyQuoteHoldsABatchThatMovedTogether(t *testing.T) {
	ctx := context.Background()
	baseline := map[string]money.Rate{"USD": "0.8", "PLN": "0.25", "GBP": "1.25"}

	// The inversion error: every rate lands on the wrong side of one.
	l := newLedger(t)
	typed(t, l, "2026-09-01", baseline)
	typed(t, l, "2026-09-18", map[string]money.Rate{"CHF": "1.05"})
	got, err := l.ApplyQuote(ctx, quote(map[string]money.Rate{"USD": "0.8", "PLN": "0.25", "GBP": "1.25", "CHF": "1"}), "ecb",
		[]string{"USD", "PLN", "GBP", "CHF"})
	if err != nil {
		t.Fatal(err)
	}
	holdsOnly(t, got, "GBP", "PLN", "USD")
	if !reflect.DeepEqual(got.Kept, []string{"CHF"}) || len(got.Filed) != 0 || len(got.Notes) != 0 {
		t.Errorf("%+v", got)
	}
	if h := got.Held[1]; h.Currency != "PLN" || h.Rate != "4" || h.Previous != "0.25" || h.PreviousDate != "2026-09-01" ||
		!strings.Contains(h.Reason, "3 of 3") {
		t.Errorf("%+v", h)
	}
	if all, _ := l.Rates(ctx, ""); len(all) != 4 {
		t.Errorf("a held batch was written: %+v", all)
	}

	// One of two moving is the market: filed, and the mover named.
	l = newLedger(t)
	typed(t, l, "2026-09-01", baseline)
	got, err = l.ApplyQuote(ctx, quote(map[string]money.Rate{"USD": "0.5", "PLN": "4"}), "ecb", []string{"USD", "PLN"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Filed, []string{"PLN", "USD"}) || len(got.Held) != 0 {
		t.Errorf("%+v", got)
	}
	if len(got.Notes) != 1 || got.Notes[0] != "USD moved from 0.8 on 2026-09-01 to 2" {
		t.Errorf("notes %v", got.Notes)
	}
	if r, _, _ := l.RateOn(ctx, "USD", "2026-09-18"); r != "2" {
		t.Errorf("USD on file as %s", r)
	}

	// Two of three is more than half, and the one that stood still is held
	// with the others.
	l = newLedger(t)
	typed(t, l, "2026-09-01", baseline)
	got, err = l.ApplyQuote(ctx, quote(map[string]money.Rate{"USD": "0.5", "PLN": "2", "GBP": "0.8"}), "ecb", []string{"USD", "PLN", "GBP"})
	if err != nil {
		t.Fatal(err)
	}
	holdsOnly(t, got, "GBP", "PLN", "USD")
	if !strings.Contains(got.Held[0].Reason, "2 of 3") {
		t.Errorf("%+v", got.Held[0])
	}
}

// TestApplyQuoteHoldsOneMovingAlone: a lone currency past fourfold is held
// and named with the rate it would have replaced, the rest are filed, and
// accepting the held one files it under the source's name.
func TestApplyQuoteHoldsOneMovingAlone(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	typed(t, l, "2026-09-01", map[string]money.Rate{"USD": "0.8", "PLN": "0.25"})
	got, err := l.ApplyQuote(ctx, quote(map[string]money.Rate{"USD": "0.25", "PLN": "4"}), "ecb", []string{"USD", "PLN"})
	if err != nil {
		t.Fatal(err)
	}
	holdsOnly(t, got, "USD")
	if !reflect.DeepEqual(got.Filed, []string{"PLN"}) || len(got.Notes) != 0 {
		t.Errorf("%+v", got)
	}
	if h := got.Held[0]; h.Rate != "4" || h.Previous != "0.8" || h.PreviousDate != "2026-09-01" || !strings.Contains(h.Reason, "fourfold") {
		t.Errorf("%+v", h)
	}
	if r, _, _ := l.RateOn(ctx, "USD", "2026-09-18"); r != "0.8" {
		t.Errorf("the held rate was filed: %s", r)
	}

	out, err := l.AcceptRate(ctx, "usd", "4", "2026-09-18", "ecb")
	if err != nil {
		t.Fatal(err)
	}
	if out.Currency != "USD" || out.Source != "ecb" || out.Rederived != 0 {
		t.Errorf("%+v", out)
	}
	if list, _ := l.Rates(ctx, "USD"); len(list) != 2 || list[0].Rate != "4" || list[0].Source != "ecb" {
		t.Errorf("%+v", list)
	}
	if _, err := l.AcceptRate(ctx, "USD", "4", "2026-09-18", SourceManual); err == nil {
		t.Error("a typed source was accepted")
	}
	if _, err := l.AcceptRate(ctx, "EUR", "1", "2026-09-18", "ecb"); err == nil {
		t.Error("the reference was accepted")
	}
}

// TestApplyQuoteSkipsAnOldBaseline: a baseline more than ninety days from
// the publication is no comparison, so a tenfold move is filed without a
// word; at ninety days it is compared and held.
func TestApplyQuoteSkipsAnOldBaseline(t *testing.T) {
	ctx := context.Background()
	tenfold := quote(map[string]money.Rate{"USD": "1.25", "PLN": "4"})

	l := newLedger(t)
	typed(t, l, "2026-06-19", map[string]money.Rate{"USD": "0.08"})
	got, err := l.ApplyQuote(ctx, tenfold, "ecb", []string{"USD", "PLN"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Filed, []string{"PLN", "USD"}) || len(got.Held) != 0 || len(got.Notes) != 0 {
		t.Errorf("91 days: %+v", got)
	}

	l = newLedger(t)
	typed(t, l, "2026-06-20", map[string]money.Rate{"USD": "0.08"})
	got, err = l.ApplyQuote(ctx, tenfold, "ecb", []string{"USD", "PLN"})
	if err != nil {
		t.Fatal(err)
	}
	holdsOnly(t, got, "USD")
	if !reflect.DeepEqual(got.Filed, []string{"PLN"}) {
		t.Errorf("90 days: %+v", got)
	}

	// A baseline after the publication counts too, as the ledger would have
	// read it.
	l = newLedger(t)
	typed(t, l, "2026-10-01", map[string]money.Rate{"USD": "0.08"})
	got, err = l.ApplyQuote(ctx, tenfold, "ecb", []string{"USD"})
	if err != nil {
		t.Fatal(err)
	}
	holdsOnly(t, got, "USD")
	if got.Held[0].PreviousDate != "2026-10-01" {
		t.Errorf("%+v", got.Held[0])
	}
}

// TestApplyQuoteRederives: filing moves every row in the currency that
// follows the table, at the table's rate for its own date, and counts them.
func TestApplyQuoteRederives(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	wallet := mustAccount(t, l, "Wallet", Cash, "PLN", 0)
	typed(t, l, "2026-09-01", map[string]money.Rate{"PLN": "0.25"})
	add := func(date string, fxRate money.Rate) Transaction {
		t.Helper()
		out, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: wallet.ID, Amount: 10000, CategoryID: "Fuel", Date: date, FXRate: fxRate})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before, after, pinned := add("2026-09-10", ""), add("2026-09-20", ""), add("2026-09-20", "0.3")
	got, err := l.ApplyQuote(ctx, feed.Builtin, "ecb", []string{"PLN"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Filed, []string{"PLN"}) || got.Rederived != 2 {
		t.Errorf("%+v", got)
	}
	// 100.00 PLN at 0.22917383 is 22.92 EUR.
	if r, _ := l.Get(ctx, after.ID); r.ReferenceAmount != -2292 {
		t.Errorf("after: %d", r.ReferenceAmount)
	}
	if r, _ := l.Get(ctx, before.ID); r.ReferenceAmount != -2500 {
		t.Errorf("before: %d", r.ReferenceAmount)
	}
	if r, _ := l.Get(ctx, pinned.ID); r.ReferenceAmount != -3000 || r.FXRate != "0.3" {
		t.Errorf("pinned: %+v", r)
	}
}

// TestCurrenciesInUse: what accounts and transactions carry and what plans
// are filed in, the reference left out, sorted, and empty rather than nil.
func TestCurrenciesInUse(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	if got, err := l.CurrenciesInUse(ctx); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	mustAccount(t, l, "Dollars", Checking, "USD", 0)
	typed(t, l, "2026-01-01", map[string]money.Rate{"PLN": "0.25", "GBP": "1.2", "CHF": "1"})
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: main.ID, Amount: 100, Currency: "PLN", CategoryID: "Fuel"}); err != nil {
		t.Fatal(err)
	}
	if err := l.SetBudget(ctx, "Groceries", "2026-09", 10000, "GBP"); err != nil {
		t.Fatal(err)
	}
	if err := l.SetBudget(ctx, "Fuel", "2026-09", 5000, "CHF"); err != nil {
		t.Fatal(err)
	}
	if err := l.SetBudget(ctx, "Fuel", "2026-09", 0, "CHF"); err != nil {
		t.Fatal(err)
	}
	got, err := l.CurrenciesInUse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"GBP", "PLN", "USD"}) {
		t.Errorf("%v", got)
	}
}

// TestFetchStamp: nothing on record until a fetch runs, then when, which
// source and which host, replaced by the next.
func TestFetchStamp(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	if _, ok, err := l.LastFetch(ctx); err != nil || ok {
		t.Fatalf("fresh ledger: %v %v", ok, err)
	}
	if err := l.StampFetch(ctx, "ecb", "fx.example"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := l.LastFetch(ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got != (Fetch{At: "2026-09-13T12:00:00Z", Source: "ecb", Host: "fx.example"}) {
		t.Errorf("%+v", got)
	}
	l.now = func() time.Time { return time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC) }
	if err := l.StampFetch(ctx, "frankfurter", "rates.example"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := l.LastFetch(ctx); got != (Fetch{At: "2026-09-14T08:00:00Z", Source: "frankfurter", Host: "rates.example"}) {
		t.Errorf("%+v", got)
	}
	if _, err := l.DB().Meta(ctx, db.MetaRateSeeded("USD")); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("a stamp touched the seed markers: %v", err)
	}
}
