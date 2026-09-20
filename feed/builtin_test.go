package feed

import (
	"testing"
)

// TestBuiltinMatchesFixture: the shipped quote is what the bank's kept
// response says, number for number, so it can only change by regenerating.
func TestBuiltinMatchesFixture(t *testing.T) {
	q := fixture(t, ECB, "ecb-daily.xml")
	if Builtin.Base != q.Base || Builtin.Date != q.Date {
		t.Fatalf("builtin is %s on %s, the fixture %s on %s", Builtin.Base, Builtin.Date, q.Base, q.Date)
	}
	if len(Builtin.Rates) != len(q.Rates) {
		t.Fatalf("builtin has %d rates, the fixture %d", len(Builtin.Rates), len(q.Rates))
	}
	for currency, rate := range q.Rates {
		if Builtin.Rates[currency] != rate {
			t.Errorf("%s: builtin %s, fixture %s", currency, Builtin.Rates[currency], rate)
		}
	}
	// And the other transport carries the same set.
	ff := fixture(t, Frankfurter, "frankfurter.json")
	for currency := range Builtin.Rates {
		if _, ok := ff.Rates[currency]; !ok {
			t.Errorf("%s is shipped but not on the second source", currency)
		}
	}
	for currency := range ff.Rates {
		if _, ok := Builtin.Rates[currency]; !ok {
			t.Errorf("%s is on the second source but not shipped", currency)
		}
	}
}

// TestBuiltinIsWellFormed pins the shape: fiat only, three upper-case
// letters each, the base not among them, and usable against any of them.
func TestBuiltinIsWellFormed(t *testing.T) {
	if err := Builtin.check(); err != nil {
		t.Fatal(err)
	}
	if Builtin.Base != "EUR" {
		t.Errorf("base %s", Builtin.Base)
	}
	if _, ok := Builtin.Rates["EUR"]; ok {
		t.Error("the base is among the rates")
	}
	for _, crypto := range []string{"BTC", "DCR", "LTC", "ETH"} {
		if _, ok := Builtin.Rates[crypto]; ok {
			t.Errorf("%s is shipped: neither source quotes it", crypto)
		}
	}
	for currency := range Builtin.Rates {
		if !code.MatchString(currency) {
			t.Errorf("%q is not a currency code", currency)
		}
	}
	for _, reference := range []string{"EUR", "USD", "GBP", "PLN", "JPY"} {
		rates, err := Builtin.Against(reference)
		if err != nil {
			t.Errorf("against %s: %v", reference, err)
			continue
		}
		if len(rates) != len(Builtin.Rates) {
			t.Errorf("against %s: %d rates, want %d", reference, len(rates), len(Builtin.Rates))
		}
	}
}
