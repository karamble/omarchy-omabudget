package api

import (
	"errors"
	"net/http"

	"github.com/karamble/omarchy-omabudget/money"
)

// Reconciliation: a statement held against the ledger. The sheet is a read;
// settling it is a write; ticking a line is the ordinary transaction edit,
// since a line's place on a statement is its status.

type reconcileIn struct {
	Account   string `json:"account"`
	Through   string `json:"through,omitempty"`
	Statement string `json:"statement"`
}

// handleReconcile builds the sheet without writing anything.
func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	account := q.Get("account")
	if account == "" {
		s.fail(w, errors.New("which account is this statement for?"))
		return
	}
	a, err := l.Account(r.Context(), account)
	if err != nil {
		s.fail(w, err)
		return
	}
	statement, err := money.Parse(q.Get("statement"), a.Currency)
	if err != nil && !errors.Is(err, money.ErrZero) {
		s.fail(w, err)
		return
	}
	out, err := l.Reconcile(r.Context(), a.ID, q.Get("through"), statement.Minor)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// handleFinishReconcile settles the sheet, and refuses while it is out.
func (s *Server) handleFinishReconcile(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in reconcileIn
	if !s.decode(w, r, &in) {
		return
	}
	if in.Account == "" {
		s.fail(w, errors.New("which account is this statement for?"))
		return
	}
	a, err := l.Account(r.Context(), in.Account)
	if err != nil {
		s.fail(w, err)
		return
	}
	statement, err := money.Parse(in.Statement, a.Currency)
	if err != nil && !errors.Is(err, money.ErrZero) {
		s.fail(w, err)
		return
	}
	sheet, n, err := l.FinishReconcile(r.Context(), a.ID, in.Through, statement.Minor)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Sheet      any `json:"sheet"`
		Reconciled int `json:"reconciled"`
	}{sheet, n})
}
