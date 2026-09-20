package feed

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"

	"github.com/karamble/omarchy-omabudget/money"
)

// Frankfurter serves the same reference rates as JSON. The endpoint is
// plain latest, with no base or symbols, so the request says nothing about
// what the user holds.
var Frankfurter = Source{
	ID:     "frankfurter",
	Name:   "Frankfurter",
	What:   "The same reference rates as JSON, from the public instance or one you run yourself.",
	URL:    "https://api.frankfurter.dev/v1/latest",
	Custom: true,
	parse:  parseFrankfurter,
}

// frankfurterLatest is the latest document. Numbers are kept as written.
type frankfurterLatest struct {
	Amount json.Number            `json:"amount"`
	Base   string                 `json:"base"`
	Date   string                 `json:"date"`
	Rates  map[string]json.Number `json:"rates"`
}

// parseFrankfurter reads the JSON with UseNumber, so no value goes through
// a float on the way in.
func parseFrankfurter(r io.Reader) (Quote, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var doc frankfurterLatest
	if err := dec.Decode(&doc); err != nil {
		return Quote{}, err
	}
	// Rates are per amount of the base; only one unit is a quote.
	if doc.Amount != "" {
		amount, ok := new(big.Rat).SetString(string(doc.Amount))
		if !ok || amount.Cmp(big.NewRat(1, 1)) != 0 {
			return Quote{}, fmt.Errorf("rates are per %s of the base, not per 1", doc.Amount)
		}
	}
	q := Quote{Base: doc.Base, Date: doc.Date, Rates: make(map[string]money.Rate, len(doc.Rates))}
	for currency, n := range doc.Rates {
		q.Rates[currency] = money.Rate(n)
	}
	return q, nil
}
