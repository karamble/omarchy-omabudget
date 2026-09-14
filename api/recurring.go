package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/money"
)

// ruleIn is a rule as typed: the amount as text, references by name or id.
type ruleIn struct {
	Name            string   `json:"name"`
	Kind            string   `json:"kind,omitempty"`
	Account         string   `json:"account,omitempty"`
	CounterAccount  string   `json:"counterAccount,omitempty"`
	Amount          string   `json:"amount"`
	Category        string   `json:"category,omitempty"`
	Description     string   `json:"description,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Frequency       string   `json:"frequency"`
	IntervalDays    int      `json:"intervalDays,omitempty"`
	DayRule         string   `json:"dayRule,omitempty"`
	StartDate       string   `json:"startDate"`
	EndDate         string   `json:"endDate,omitempty"`
	OccurrenceCount int      `json:"occurrenceCount,omitempty"`
	AutoPost        bool     `json:"autoPost"`
	LeadDays        *int     `json:"leadDays,omitempty"`
	VariableAmount  bool     `json:"variableAmount"`
	Active          *bool    `json:"active,omitempty"`
}

// billRow is an occurrence with its names filled in for display.
type billRow struct {
	domain.Due
	CategoryName string `json:"categoryName"`
	CategoryIcon string `json:"categoryIcon"`
	AccountName  string `json:"accountName"`
}

func (s *Server) buildRule(ctx context.Context, l *domain.Ledger, in ruleIn) (domain.Rule, error) {
	kind := domain.Kind(strings.ToLower(in.Kind))
	if kind == "" {
		kind = domain.Expense
	}
	accountRef := in.Account
	if accountRef == "" {
		accountRef = s.defaultAccount(ctx, l)
	}
	if accountRef == "" {
		return domain.Rule{}, errors.New("no account: add one first")
	}
	acct, err := l.Account(ctx, accountRef)
	if err != nil {
		return domain.Rule{}, err
	}
	amount, err := money.Parse(in.Amount, acct.Currency)
	if err != nil {
		return domain.Rule{}, err
	}
	lead := 3
	if in.LeadDays != nil {
		lead = *in.LeadDays
	}
	r := domain.Rule{
		Name: in.Name,
		Template: domain.Template{
			Kind: kind, AccountID: acct.ID, CounterAccountID: in.CounterAccount, Amount: amount.Minor,
			Currency: acct.Currency, CategoryID: in.Category, Description: in.Description, Tags: in.Tags,
		},
		Frequency: in.Frequency, IntervalDays: in.IntervalDays, DayRule: in.DayRule,
		StartDate: in.StartDate, EndDate: in.EndDate, OccurrenceCount: in.OccurrenceCount,
		AutoPost: in.AutoPost, LeadDays: lead, VariableAmount: in.VariableAmount, Active: true,
	}
	if r.StartDate == "" {
		r.StartDate = clock().Format(dateFmt)
	}
	if in.Active != nil {
		r.Active = *in.Active
	}
	return r, nil
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	all := r.URL.Query().Get("all") == "1" || r.URL.Query().Get("all") == "true"
	rules, err := l.Rules(r.Context(), all)
	if err != nil {
		s.fail(w, err)
		return
	}
	if rules == nil {
		rules = []domain.Rule{}
	}
	writeJSON(w, s.logger, http.StatusOK, rules)
}

func (s *Server) handleAddRule(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in ruleIn
	if !s.decode(w, r, &in) {
		return
	}
	rule, err := s.buildRule(r.Context(), l, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	out, err := l.AddRule(r.Context(), rule)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, out)
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in ruleIn
	if !s.decode(w, r, &in) {
		return
	}
	rule, err := s.buildRule(r.Context(), l, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	rule.ID = r.PathValue("id")
	out, err := l.UpdateRule(r.Context(), rule)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

func (s *Server) handleRemoveRule(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	if err := l.RemoveRule(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"removed": r.PathValue("id")})
}

// handlePostRule writes the next occurrence, on the date and for the amount
// given, or the scheduled ones.
func (s *Server) handlePostRule(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in struct {
		Date   string `json:"date,omitempty"`
		Amount string `json:"amount,omitempty"`
	}
	if r.ContentLength != 0 && !s.decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	var amount int64
	if strings.TrimSpace(in.Amount) != "" {
		rule, err := l.Rule(ctx, r.PathValue("id"))
		if err != nil {
			s.fail(w, err)
			return
		}
		a, err := money.Parse(in.Amount, rule.Template.Currency)
		if err != nil {
			s.fail(w, err)
			return
		}
		amount = a.Minor
	}
	out, err := l.Post(ctx, r.PathValue("id"), in.Date, amount)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, out)
}

func (s *Server) handleSkipRule(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	out, err := l.Skip(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// handleBills lists what is due in the next days (30 unless asked).
func (s *Server) handleBills(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	days := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && n > 0 && n <= 366 {
		days = n
	}
	rows, err := s.bills(r.Context(), l, days, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, rows)
}

// bills is the upcoming list with names, at most limit rows when limit is
// set.
func (s *Server) bills(ctx context.Context, l *domain.Ledger, days, limit int) ([]billRow, error) {
	today := clock()
	due, err := l.Upcoming(ctx, today.Format(dateFmt), today.AddDate(0, 0, days).Format(dateFmt))
	if err != nil {
		return nil, err
	}
	index, err := l.CategoryIndex(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := l.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, a := range accounts {
		names[a.ID] = a.Name
	}
	out := make([]billRow, 0, len(due))
	for _, d := range due {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, billRow{
			Due: d, CategoryName: index.Name(d.CategoryID), CategoryIcon: index.Icon(d.CategoryID),
			AccountName: names[d.AccountID],
		})
	}
	return out, nil
}
