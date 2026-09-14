package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/mcpserver"
)

// The MCP tools read through the same builders as the HTTP handlers, so an
// agent and the app never see different figures.

var errNoLedger = errors.New("the ledger is not open")

func (s *Server) openLedger() (*domain.Ledger, error) {
	l := s.Ledger()
	if l == nil {
		return nil, errNoLedger
	}
	return l, nil
}

func (s *Server) mcpDashboard(ctx context.Context) (any, error) {
	l, err := s.openLedger()
	if err != nil {
		return nil, err
	}
	return s.dashboard(ctx, l)
}

func (s *Server) mcpTransactions(ctx context.Context, q mcpserver.TransactionQuery) (any, error) {
	l, err := s.openLedger()
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	account := q.Account
	if account != "" {
		a, err := l.Account(ctx, account)
		if err != nil {
			return nil, err
		}
		account = a.ID
	}
	list, err := l.Transactions(ctx, domain.Filter{
		Search: q.Search, Kind: domain.Kind(q.Kind), AccountID: account, CategoryID: q.Category,
		From: q.From, To: q.To, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.Transaction{}
	}
	return list, nil
}

func (s *Server) mcpAddTransaction(ctx context.Context, in map[string]any) (any, error) {
	l, err := s.openLedger()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var form transactionIn
	if err := json.Unmarshal(raw, &form); err != nil {
		return nil, err
	}
	t, err := s.buildTransaction(ctx, l, form)
	if err != nil {
		return nil, err
	}
	return l.Add(ctx, t)
}

func (s *Server) mcpBudget(ctx context.Context, period string) (any, error) {
	l, err := s.openLedger()
	if err != nil {
		return nil, err
	}
	sp, err := s.spanFor(period)
	if err != nil {
		return nil, err
	}
	return l.Budgets(ctx, sp.key(), sp.from(), sp.to())
}

func (s *Server) mcpBills(ctx context.Context, days int) (any, error) {
	l, err := s.openLedger()
	if err != nil {
		return nil, err
	}
	if days > 366 {
		days = 366
	}
	return s.bills(ctx, l, days, 0)
}

func (s *Server) mcpSpending(ctx context.Context, period string) (any, error) {
	l, err := s.openLedger()
	if err != nil {
		return nil, err
	}
	return s.spendingFor(ctx, l, period)
}
