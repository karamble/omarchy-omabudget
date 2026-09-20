package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/karamble/omarchy-omabudget/money"
)

// The rate table, spec 8. Rates are entered, never fetched: this app opens no
// outward connection.

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
