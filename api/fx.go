package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/feed"
	"github.com/karamble/omarchy-omabudget/money"
)

// The rate table, spec 8. Rates are typed, or fetched from the source chosen
// in settings when a person presses Fetch now or runs the rate fetch
// command. That fetch is the one connection this daemon opens outward, and
// nothing here makes it on its own: not at start, not on a timer, not for a
// missing rate.

func (s *Server) handleRates(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	rates, err := l.Rates(r.Context(), r.URL.Query().Get("currency"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, rates)
}

type rateIn struct {
	Currency string `json:"currency"`
	Rate     string `json:"rate"`
	Date     string `json:"date,omitempty"` // today when empty
}

func (s *Server) handleSetRate(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in rateIn
	if !s.decode(w, r, &in) {
		return
	}
	out, err := l.SetRate(r.Context(), in.Currency, money.Rate(in.Rate), in.Date)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

func (s *Server) handleRemoveRate(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	currency, date := strings.ToUpper(r.PathValue("currency")), r.PathValue("date")
	// Figures are shown in the base at its rate, so the base keeps its last
	// one until another is filed or the base moves.
	if base := s.Config().BaseCurrency; currency == base && base != l.Reference() {
		filed, err := l.Rates(r.Context(), currency)
		if err != nil {
			s.fail(w, err)
			return
		}
		if len(filed) == 1 && filed[0].Date == date {
			s.fail(w, fmt.Errorf("%s is the base currency and this is its last rate: file another or change the base first", currency))
			return
		}
	}
	if err := l.RemoveRate(r.Context(), currency, date); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"removed": currency + " " + date})
}

// fetchTimeout is what one fetch gets. The panel gives a helper fifteen
// seconds and the CLI waits thirty, so the daemon answers before either
// gives up.
const fetchTimeout = 10 * time.Second

// sourceFor resolves a source ID and a URL as chosen: an empty ID is the
// first in the registry, an empty URL the source's own endpoint. A URL on a
// source that reads from one place, or one a fetch would refuse, is an
// error, so a bad choice is refused when it is made.
func sourceFor(id, rawURL string) (feed.Source, error) {
	src := feed.Sources[0]
	if id != "" {
		s, ok := feed.Lookup(id)
		if !ok {
			return feed.Source{}, fmt.Errorf("rate source %q is not known: omabudget rate sources lists them", id)
		}
		src = s
	}
	if rawURL == "" {
		return src, nil
	}
	if !src.Custom {
		return feed.Source{}, fmt.Errorf("%s reads from one place: a url is for a source you run yourself", src.Name)
	}
	if err := feed.CheckURL(rawURL); err != nil {
		return feed.Source{}, err
	}
	return src.At(rawURL), nil
}

// hostOf names who a fetch from a source connects to.
func hostOf(src feed.Source) string {
	u, err := url.Parse(src.URL)
	if err != nil || u.Host == "" {
		return src.URL
	}
	return u.Host
}

// fetchIn overrides the chosen source for one press, unsaved. A source
// given without a URL reads from its own endpoint.
type fetchIn struct {
	Source string `json:"source"`
	URL    string `json:"url"`
}

// fetchOut is what one press did: where it read from, what the source
// published, the rates it quoted for the currencies in use, and what became
// of each.
type fetchOut struct {
	Source    string                `json:"source"`
	Name      string                `json:"name"`
	Host      string                `json:"host"`
	Published string                `json:"published"`
	Rates     map[string]money.Rate `json:"rates"`
	domain.Applied
}

// handleFetchRates reads the chosen source once and files what it quoted
// for the currencies in use. The record of the fetch is written before the
// request, whatever comes of it, since the connection is made either way.
func (s *Server) handleFetchRates(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in fetchIn
	if r.ContentLength != 0 && !s.decode(w, r, &in) {
		return
	}
	cfg := s.Config()
	id, rawURL := cfg.RateSource, cfg.RateSourceURL
	if in.Source != "" {
		id, rawURL = in.Source, ""
	}
	if in.URL != "" {
		rawURL = in.URL
	}
	src, err := sourceFor(id, rawURL)
	if err != nil {
		s.fail(w, err)
		return
	}
	ctx := r.Context()
	host := hostOf(src)
	if err := l.StampFetch(ctx, src.ID, host); err != nil {
		s.fail(w, err)
		return
	}
	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	q, err := s.fetch(fctx, src)
	cancel()
	if err != nil {
		writeJSON(w, s.logger, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("no rates from %s: %v", host, err)})
		return
	}
	use, err := l.CurrenciesInUse(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	// Figures are shown in the base, so it needs a rate like an account does.
	use = append(use, strings.ToUpper(cfg.BaseCurrency))
	applied, err := l.ApplyQuote(ctx, q, src.ID, use)
	if err != nil {
		s.fail(w, err)
		return
	}
	quoted, err := q.Against(l.Reference())
	if err != nil {
		s.fail(w, err)
		return
	}
	rates := map[string]money.Rate{}
	for _, currency := range use {
		if rate, ok := quoted[currency]; ok {
			rates[currency] = rate
		}
	}
	writeJSON(w, s.logger, http.StatusOK, fetchOut{
		Source: src.ID, Name: src.Name, Host: host, Published: q.Date, Rates: rates, Applied: applied,
	})
}

type acceptIn struct {
	Currency string `json:"currency"`
	Rate     string `json:"rate"`
	Date     string `json:"date"`
	Source   string `json:"source"`
}

// handleAcceptRate files one rate a fetch held, stamped with the source
// that quoted it. Nothing is fetched: the rate arrives in the request.
func (s *Server) handleAcceptRate(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in acceptIn
	if !s.decode(w, r, &in) {
		return
	}
	if _, ok := feed.Lookup(in.Source); !ok {
		s.fail(w, fmt.Errorf("source %q is not a rate source", in.Source))
		return
	}
	out, err := l.AcceptRate(r.Context(), in.Currency, money.Rate(in.Rate), in.Date, in.Source)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// sourceOut describes one source to a person choosing it: its name, who is
// on the other end of the request, where it reads from, and whether that
// can be an instance of their own.
type sourceOut struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	What   string `json:"what"`
	URL    string `json:"url"`
	Custom bool   `json:"custom"`
}

func (s *Server) handleRateSources(w http.ResponseWriter, r *http.Request) {
	out := make([]sourceOut, 0, len(feed.Sources))
	for _, src := range feed.Sources {
		out = append(out, sourceOut{ID: src.ID, Name: src.Name, What: src.What, URL: src.URL, Custom: src.Custom})
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}
