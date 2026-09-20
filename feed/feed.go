// Package feed reads exchange rates published by a source the user picks.
//
// A quote is what the source published: one unit of its base in every
// currency it lists, on the date it published. Against turns that round to
// the ledger's convention. fetch.go is the only file in the module that
// opens a connection outward, and it runs only when a person asks.
package feed

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"time"

	"github.com/karamble/omarchy-omabudget/money"
)

// Quote is one publication: 1 Base = Rates[currency] of that currency on
// Date. The base is not among the rates.
type Quote struct {
	Base  string
	Date  string
	Rates map[string]money.Rate
}

// Against restates the quote as the rate table keeps it, 1 currency = x
// reference, crossing through the base when the reference is not the base.
// The base is then quoted like any other currency, and the reference is
// not. The arithmetic is exact until the final decimal.
func (q Quote) Against(reference string) (map[string]money.Rate, error) {
	inBase := func(currency string) (*big.Rat, error) {
		if currency == q.Base {
			return big.NewRat(1, 1), nil
		}
		raw, ok := q.Rates[currency]
		if !ok {
			return nil, fmt.Errorf("%s is not quoted", currency)
		}
		r, ok := new(big.Rat).SetString(string(raw))
		if !ok || r.Sign() <= 0 {
			return nil, fmt.Errorf("%s rate %q is not a positive number", currency, raw)
		}
		// 1 base = r currency, so 1 currency = 1/r base.
		return r.Inv(r), nil
	}
	ref, err := inBase(reference)
	if err != nil {
		return nil, err
	}
	out := make(map[string]money.Rate, len(q.Rates)+1)
	for currency := range q.Rates {
		if currency == reference {
			continue
		}
		v, err := inBase(currency)
		if err != nil {
			return nil, err
		}
		out[currency] = money.RateOf(v.Quo(v, ref))
	}
	if reference != q.Base {
		out[q.Base] = money.RateOf(new(big.Rat).Inv(ref))
	}
	return out, nil
}

var code = regexp.MustCompile(`^[A-Z]{3}$`)

// check refuses a quote that is not the shape a source publishes: a dated
// base, at least one rate, three-letter codes, positive numbers.
func (q Quote) check() error {
	if !code.MatchString(q.Base) {
		return fmt.Errorf("base %q is not a currency code", q.Base)
	}
	if _, err := time.Parse("2006-01-02", q.Date); err != nil {
		return fmt.Errorf("date %q is not YYYY-MM-DD", q.Date)
	}
	if len(q.Rates) == 0 {
		return errors.New("no rates in the response")
	}
	for currency, raw := range q.Rates {
		if !code.MatchString(currency) {
			return fmt.Errorf("%q is not a currency code", currency)
		}
		if currency == q.Base {
			return fmt.Errorf("the base %s is quoted against itself", currency)
		}
		r, ok := new(big.Rat).SetString(string(raw))
		if !ok || r.Sign() <= 0 {
			return fmt.Errorf("%s rate %q is not a positive number", currency, raw)
		}
	}
	return nil
}

// Source is one place a quote can be read from.
type Source struct {
	// ID is what a rate filed from here is stamped with.
	ID string
	// Name and What describe it to a person choosing one: the name, and
	// who is on the other end of the request.
	Name string
	What string
	// URL is the endpoint. Custom marks a source that accepts a URL of the
	// user's own, for an instance they run themselves.
	URL    string
	Custom bool
	parse  func(io.Reader) (Quote, error)
}

// At is the source pointed at another endpoint.
func (s Source) At(rawURL string) Source {
	s.URL = rawURL
	return s
}

// Parse reads a response body into a checked quote.
func (s Source) Parse(r io.Reader) (Quote, error) {
	q, err := s.parse(r)
	if err != nil {
		return Quote{}, fmt.Errorf("%s: %w", s.Name, err)
	}
	if err := q.check(); err != nil {
		return Quote{}, fmt.Errorf("%s: %w", s.Name, err)
	}
	return q, nil
}

// Sources lists every source, in the order a picker shows them.
var Sources = []Source{ECB, Frankfurter}

// Lookup finds a source by ID.
func Lookup(id string) (Source, bool) {
	for _, s := range Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}
