package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/money"
)

// ledgerOr answers 503 when the daemon came up without a ledger, so no
// handler dereferences a nil.
func (s *Server) ledgerOr(w http.ResponseWriter) (*domain.Ledger, bool) {
	l := s.Ledger()
	if l == nil {
		writeJSON(w, s.logger, http.StatusServiceUnavailable, map[string]string{"error": "the ledger is not open"})
		return nil, false
	}
	return l, true
}

// fail maps domain errors to statuses: not found is 404, a rule the spec says
// to block on is 422, anything else 500.
func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeJSON(w, s.logger, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, money.ErrZero), errors.Is(err, money.ErrEmpty), errors.Is(err, money.ErrTooFine),
		errors.Is(err, money.ErrMalformed), errors.Is(err, money.ErrCurrency):
		writeJSON(w, s.logger, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	default:
		// Domain validation returns plain errors; report them as the client's
		// problem rather than the server's.
		writeJSON(w, s.logger, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	}
}

// ---- accounts

type accountRow struct {
	domain.Account
	Balance int64 `json:"balance"`
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	accounts, err := l.Accounts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	rows := make([]accountRow, 0, len(accounts))
	for _, a := range accounts {
		b, err := l.Balance(r.Context(), a.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		rows = append(rows, accountRow{Account: a, Balance: b.Minor})
	}
	writeJSON(w, s.logger, http.StatusOK, rows)
}

type accountIn struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Currency       string `json:"currency"`
	OpeningBalance string `json:"openingBalance,omitempty"`
	OpeningDate    string `json:"openingDate,omitempty"`
	Institution    string `json:"institution,omitempty"`
	Last4          string `json:"last4,omitempty"`
	NetWorth       *bool  `json:"includeInNetWorth,omitempty"`
	LowBalance     string `json:"lowBalance,omitempty"`
}

func (s *Server) handleAddAccount(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in accountIn
	if !s.decode(w, r, &in) {
		return
	}
	a := domain.Account{
		Name: in.Name, Type: domain.AccountType(in.Type), Currency: in.Currency,
		OpeningDate: in.OpeningDate, Institution: in.Institution, Last4: in.Last4,
		IncludeInNetWorth: in.NetWorth == nil || *in.NetWorth,
	}
	if strings.TrimSpace(in.OpeningBalance) != "" {
		opening, err := money.Parse(in.OpeningBalance, in.Currency)
		if err != nil && !errors.Is(err, money.ErrZero) {
			s.fail(w, err)
			return
		}
		a.OpeningBalance = opening.Minor
	}
	if strings.TrimSpace(in.LowBalance) != "" {
		low, err := money.Parse(in.LowBalance, in.Currency)
		if err != nil && !errors.Is(err, money.ErrZero) {
			s.fail(w, err)
			return
		}
		v := low.Minor
		a.LowBalance = &v
	}
	out, err := l.AddAccount(r.Context(), a)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, out)
}

// accountEdit changes only the fields it carries.
type accountEdit struct {
	Name           *string `json:"name"`
	Type           *string `json:"type"`
	Institution    *string `json:"institution"`
	Last4          *string `json:"last4"`
	Active         *bool   `json:"active"`
	NetWorth       *bool   `json:"includeInNetWorth"`
	LowBalance     *string `json:"lowBalance"` // an amount, or "" to clear
	OpeningBalance *string `json:"opening"`
	OpeningDate    *string `json:"openingDate"`
	SortOrder      *int    `json:"sortOrder"`
	Colour         *string `json:"colour"`
	Icon           *string `json:"icon"`
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in accountEdit
	if !s.decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	a, err := l.Account(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if in.Name != nil {
		a.Name = *in.Name
	}
	if in.Type != nil {
		a.Type = domain.AccountType(*in.Type)
	}
	if in.Institution != nil {
		a.Institution = *in.Institution
	}
	if in.Last4 != nil {
		a.Last4 = *in.Last4
	}
	if in.Active != nil {
		a.Active = *in.Active
	}
	if in.NetWorth != nil {
		a.IncludeInNetWorth = *in.NetWorth
	}
	if in.OpeningDate != nil {
		a.OpeningDate = *in.OpeningDate
	}
	if in.SortOrder != nil {
		a.SortOrder = *in.SortOrder
	}
	if in.Colour != nil {
		a.Colour = *in.Colour
	}
	if in.Icon != nil {
		a.Icon = *in.Icon
	}
	if in.LowBalance != nil {
		a.LowBalance = nil
		if strings.TrimSpace(*in.LowBalance) != "" {
			v, err := money.Parse(*in.LowBalance, a.Currency)
			if err != nil && !errors.Is(err, money.ErrZero) {
				s.fail(w, err)
				return
			}
			m := v.Minor
			a.LowBalance = &m
		}
	}
	if in.OpeningBalance != nil {
		v, err := money.Parse(*in.OpeningBalance, a.Currency)
		if err != nil && !errors.Is(err, money.ErrZero) {
			s.fail(w, err)
			return
		}
		a.OpeningBalance = v.Minor
	}
	out, err := l.UpdateAccount(ctx, a)
	if err != nil {
		s.fail(w, err)
		return
	}
	b, err := l.Balance(ctx, out.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, accountRow{Account: out, Balance: b.Minor})
}

// handleRemoveAccount deletes one nothing points at. An account with history
// is closed instead, which the domain says in its refusal.
func (s *Server) handleRemoveAccount(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := l.RemoveAccount(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"removed": id})
}

// ---- categories

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	cats, err := l.Categories(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.lensFor(r.Context(), l)
	if err != nil {
		s.fail(w, err)
		return
	}
	cats = v.categories(cats)
	if err := v.done(); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, cats)
}

// ---- transactions

// splitIn is one line as typed: an amount string, not minor units.
type splitIn struct {
	Category string `json:"category"`
	Amount   string `json:"amount"`
	Note     string `json:"note,omitempty"`
}

// transactionIn is quick-add's shape, spec 4.1: amount and category are the
// minimum, everything else defaults. Amounts arrive as text so the panel and
// the CLI send exactly what was typed, arithmetic included, and one place
// parses it.
type transactionIn struct {
	Kind           string    `json:"kind,omitempty"` // expense unless told otherwise
	Account        string    `json:"account,omitempty"`
	CounterAccount string    `json:"counterAccount,omitempty"`
	Amount         string    `json:"amount"`
	CounterAmount  string    `json:"counterAmount,omitempty"`
	Currency       string    `json:"currency,omitempty"`
	FXRate         string    `json:"fxRate,omitempty"`
	Category       string    `json:"category,omitempty"`
	Payee          string    `json:"payee,omitempty"`
	Date           string    `json:"date,omitempty"`
	Description    string    `json:"description,omitempty"`
	Notes          string    `json:"notes,omitempty"`
	Tags           []string  `json:"tags,omitempty"`
	Splits         []splitIn `json:"splits,omitempty"`
	Status         string    `json:"status,omitempty"`
}

func (s *Server) handleAddTransaction(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in transactionIn
	if !s.decode(w, r, &in) {
		return
	}
	t, err := s.buildTransaction(r.Context(), l, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	out, err := l.Add(r.Context(), t)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, out)
}

func (s *Server) handleGetTransaction(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	t, err := l.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, t)
}

// handleUpdateTransaction takes the whole form again, like a new entry, and
// writes it over the named row.
func (s *Server) handleUpdateTransaction(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in transactionIn
	if !s.decode(w, r, &in) {
		return
	}
	t, err := s.buildTransaction(r.Context(), l, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	t.ID = r.PathValue("id")
	out, err := l.Update(r.Context(), t)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// buildTransaction turns the typed form into a transaction for the ledger,
// parsing every amount in the account's currency.
func (s *Server) buildTransaction(ctx context.Context, l *domain.Ledger, in transactionIn) (domain.Transaction, error) {
	kind := domain.Kind(strings.ToLower(in.Kind))
	if kind == "" {
		kind = domain.Expense
	}
	accountRef := in.Account
	if accountRef == "" {
		accountRef = s.defaultAccount(ctx, l)
	}
	if accountRef == "" {
		return domain.Transaction{}, errors.New("no account: add one first")
	}
	acct, err := l.Account(ctx, accountRef)
	if err != nil {
		return domain.Transaction{}, err
	}
	currency := in.Currency
	if currency == "" {
		currency = acct.Currency
	}
	amount, err := money.Parse(in.Amount, currency)
	if err != nil {
		return domain.Transaction{}, err
	}

	t := domain.Transaction{
		Kind: kind, AccountID: acct.ID, CounterAccountID: in.CounterAccount,
		Amount: amount.Minor, Currency: currency, FXRate: money.Rate(in.FXRate),
		CategoryID: in.Category, PayeeName: in.Payee, Date: in.Date, Description: in.Description,
		Notes: in.Notes, Tags: in.Tags, Status: domain.Status(in.Status),
	}
	if in.CounterAmount != "" {
		counter, err := l.Account(ctx, in.CounterAccount)
		if err != nil {
			return domain.Transaction{}, err
		}
		ca, err := money.Parse(in.CounterAmount, counter.Currency)
		if err != nil {
			return domain.Transaction{}, err
		}
		v := ca.Minor
		if v < 0 {
			v = -v
		}
		t.CounterAmount = &v
	}
	for _, sp := range in.Splits {
		c, err := l.Category(ctx, sp.Category)
		if err != nil {
			return domain.Transaction{}, err
		}
		a, err := money.Parse(sp.Amount, currency)
		if err != nil {
			return domain.Transaction{}, err
		}
		t.Splits = append(t.Splits, domain.Split{CategoryID: c.ID, Amount: a.Minor, Note: sp.Note})
	}
	return t, nil
}

// defaultAccount is the account used when none is named: the first active
// checking account, else the first active one. The spec's last-used account
// arrives with settings.
func (s *Server) defaultAccount(ctx context.Context, l *domain.Ledger) string {
	accounts, err := l.Accounts(ctx)
	if err != nil {
		return ""
	}
	first := ""
	for _, a := range accounts {
		if !a.Active {
			continue
		}
		if a.Type == domain.Checking {
			return a.ID
		}
		if first == "" {
			first = a.ID
		}
	}
	return first
}

func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := domain.Filter{
		AccountID: q.Get("account"), CategoryID: q.Get("category"), PayeeID: q.Get("payee"),
		Tags: q["tag"], Kind: domain.Kind(q.Get("kind")), Status: domain.Status(q.Get("status")),
		Search: q.Get("q"), From: q.Get("from"), To: q.Get("to"),
		Deleted: q.Get("deleted") == "1" || q.Get("deleted") == "true",
	}
	// The bounds are typed in the base and the query filters on reference
	// amounts, so they convert here, before the SQL, and paging holds.
	v, err := s.lensFor(r.Context(), l)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, bound := range []struct {
		key string
		set *int64
	}{{"min", &f.Min}, {"max", &f.Max}} {
		typed, err := money.Parse(q.Get(bound.key), v.base)
		if err != nil {
			continue
		}
		if *bound.set, err = v.toReference(typed.Minor); err != nil {
			s.fail(w, err)
			return
		}
	}
	if f.PayeeID != "" {
		if p, err := l.Payee(r.Context(), f.PayeeID); err == nil {
			f.PayeeID = p.ID
		}
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		f.Limit = n
	}
	if n, err := strconv.Atoi(q.Get("offset")); err == nil && n > 0 {
		f.Offset = n
	}
	list, err := l.Transactions(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []domain.Transaction{}
	}
	writeJSON(w, s.logger, http.StatusOK, list)
}

func (s *Server) handleDeleteTransaction(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	if err := l.SoftDelete(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}

func (s *Server) handleRestoreTransaction(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	if err := l.Restore(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"restored": r.PathValue("id")})
}

// ---- dashboard

const dateFmt = "2006-01-02"

// clock is where the dashboard reads today from; tests pin it.
var clock = time.Now

type period struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`
	// Elapsed is how many of Days have passed, for the pace indicator.
	Elapsed int `json:"elapsed"`
}

// monthOut is one period of the twelve-period history.
type monthOut struct {
	Key   string `json:"key"`   // the budgets.period key, YYYY-MM
	Label string `json:"label"` // three-letter month the period begins in
	From  string `json:"from"`
	To    string `json:"to"`
	domain.Totals
}

// recentRow is a transaction with its category named for display.
type recentRow struct {
	domain.Transaction
	CategoryName string `json:"categoryName"`
	CategoryIcon string `json:"categoryIcon"`
}

type dashboardOut struct {
	// BaseCurrency is what every figure below is shown in, converted from
	// the ledger's RateReference at today's rate; a ratio is the same in
	// either. Decimals is the minor-unit digits of every currency in play.
	BaseCurrency  string            `json:"baseCurrency"`
	RateReference string            `json:"rateReference"`
	Decimals      map[string]int    `json:"decimals"`
	Period        period            `json:"period"`
	Totals        domain.Totals     `json:"totals"`
	Previous      domain.Totals     `json:"previous"`
	Liquid        int64             `json:"liquid"`
	NetWorth      int64             `json:"netWorth"`
	Accounts      []accountRow      `json:"accounts"`
	Recent        []recentRow       `json:"recent"`
	Cash          domain.CashSeries `json:"cash"`
	Budget        domain.Budget     `json:"budget"`
	Months        []monthOut        `json:"months"`
	Bills         []billRow         `json:"bills"`
	Model         string            `json:"model"`
	// Unconverted names the currencies liquid funds and net worth had to
	// leave out, because no rate is on file for them.
	Unconverted []string        `json:"unconverted,omitempty"`
	Envelopes   envelopeSummary `json:"envelopes"`
	// Insights are the two or three things worth saying about this period.
	Insights []Insight `json:"insights"`
	// DefaultAccountID is where an entry lands when none is named, so a form
	// can show the currency the amount will really be read in rather than
	// guessing at the rule.
	DefaultAccountID string `json:"defaultAccountId,omitempty"`
	// Today is the daemon's own date, which is what an undated entry gets.
	Today string `json:"today"`
	// Rates is the newest rate on file per currency, so a form can show the
	// rate an entry would read. The table itself is behind /api/rates.
	Rates []domain.FXRate `json:"rates"`
}

// envelopeSummary is the envelope model's headline: what is not yet in a
// pot, what the pots hold, and what the overspent ones are short.
type envelopeSummary struct {
	ToBeBudgeted int64 `json:"toBeBudgeted"`
	Held         int64 `json:"held"`
	Deficit      int64 `json:"deficit"`
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	out, err := s.dashboard(r.Context(), l)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// dashboard assembles the document behind the app's every screen. Every
// figure is built in the reference and shown through the lens, so the base
// can change without a single sum moving underneath.
func (s *Server) dashboard(ctx context.Context, l *domain.Ledger) (dashboardOut, error) {
	today := clock()
	current := span{periodStart(today, s.Config().PeriodStartDay)}
	p := current.bounds(today)
	v, err := s.lensFor(ctx, l)
	if err != nil {
		return dashboardOut{}, err
	}

	accounts, err := l.Accounts(ctx)
	if err != nil {
		return dashboardOut{}, err
	}
	// Liquid and net worth are undated balances, so rows dated ahead count
	// here and not in the cash series. An account in another currency is read
	// at the latest rate on file; one whose currency has no rate is left out
	// and named, rather than quietly missing.
	rates, err := l.RateTable(ctx, today.Format(dateFmt))
	if err != nil {
		return dashboardOut{}, err
	}
	out := dashboardOut{BaseCurrency: v.base, RateReference: v.reference, Period: p, Accounts: []accountRow{}}
	missing := map[string]bool{}
	for _, a := range accounts {
		b, err := l.Balance(ctx, a.ID)
		if err != nil {
			return dashboardOut{}, err
		}
		out.Accounts = append(out.Accounts, accountRow{Account: a, Balance: b.Minor})
		v, ok := domain.ToReference(b.Minor, a.Currency, l.Reference(), rates)
		if !ok {
			missing[a.Currency] = true
			continue
		}
		if a.Type.Liquid() {
			out.Liquid += v
		}
		if a.IncludeInNetWorth {
			out.NetWorth += v
		}
	}
	for c := range missing {
		out.Unconverted = append(out.Unconverted, c)
	}
	sort.Strings(out.Unconverted)
	out.Liquid, out.NetWorth = v.amount(out.Liquid), v.amount(out.NetWorth)

	// Totals over this period and the eleven before it, on reference
	// amounts, excluding transfers (P2) and categories flagged out of
	// statistics.
	out.Months = make([]monthOut, 0, 12)
	for _, sp := range spansBack(current, 12) {
		tot, err := l.Totals(ctx, sp.from(), sp.to())
		if err != nil {
			return dashboardOut{}, err
		}
		out.Months = append(out.Months, monthOut{Key: sp.key(), Label: sp.label(), From: sp.from(), To: sp.to(), Totals: v.totals(tot)})
	}
	out.Totals = out.Months[11].Totals
	out.Previous = out.Months[10].Totals

	cash, err := l.LiquidSeries(ctx, p.From, today.Format(dateFmt))
	if err != nil {
		return dashboardOut{}, err
	}
	out.Cash = v.cash(cash)
	budget, err := l.Budgets(ctx, current.key(), p.From, p.To)
	if err != nil {
		return dashboardOut{}, err
	}
	out.Budget = v.budget(budget)

	recent, err := l.Transactions(ctx, domain.Filter{Limit: 8})
	if err != nil {
		return dashboardOut{}, err
	}
	index, err := l.CategoryIndex(ctx)
	if err != nil {
		return dashboardOut{}, err
	}
	out.Recent = make([]recentRow, 0, len(recent))
	for _, t := range recent {
		out.Recent = append(out.Recent, recentRow{
			Transaction: t, CategoryName: index.Name(t.CategoryID), CategoryIcon: index.Icon(t.CategoryID),
		})
	}
	if out.Bills, err = s.bills(ctx, l, 30, 8); err != nil {
		return dashboardOut{}, err
	}
	out.Model = string(s.Config().Model)
	env, err := l.Envelopes(ctx, current.key(), p.From, p.To)
	if err != nil {
		return dashboardOut{}, err
	}
	out.Envelopes = envelopeSummary{ToBeBudgeted: v.amount(env.ToBeBudgeted), Held: v.amount(env.Held), Deficit: v.amount(env.Deficit)}
	out.DefaultAccountID = s.defaultAccount(ctx, l)
	out.Today = today.Format(dateFmt)
	// Every currency an account or a transaction carries, plus the two the
	// figures are kept and shown in, so a form knows how to write any of them.
	currencies, err := l.Currencies(ctx)
	if err != nil {
		return dashboardOut{}, err
	}
	out.Decimals = map[string]int{}
	for _, c := range append(currencies, v.base, v.reference) {
		out.Decimals[c] = money.Decimals(c)
	}
	// One row per currency rather than the rates local above, which only
	// covers currencies in use: a form previews an entry in whatever
	// currency the account is kept in.
	if out.Rates, err = l.LatestRates(ctx); err != nil {
		return dashboardOut{}, err
	}
	if err := v.done(); err != nil {
		return dashboardOut{}, err
	}
	out.Insights = insights(out)
	return out, nil
}

// ---- budgets

// spanFor resolves a YYYY-MM key, blank meaning the period holding today.
func (s *Server) spanFor(key string) (span, error) {
	startDay := s.Config().PeriodStartDay
	if key == "" {
		return span{periodStart(clock(), startDay)}, nil
	}
	return spanForKey(key, startDay)
}

func (s *Server) handleBudgets(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	sp, err := s.spanFor(r.URL.Query().Get("period"))
	if err != nil {
		s.fail(w, err)
		return
	}
	b, err := s.budgets(r.Context(), l, sp)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, b)
}

// budgets is the period's plan shown in the base.
func (s *Server) budgets(ctx context.Context, l *domain.Ledger, sp span) (domain.Budget, error) {
	b, err := l.Budgets(ctx, sp.key(), sp.from(), sp.to())
	if err != nil {
		return domain.Budget{}, err
	}
	v, err := s.lensFor(ctx, l)
	if err != nil {
		return domain.Budget{}, err
	}
	b = v.budget(b)
	return b, v.done()
}

// budgetIn is one line of the plan as typed: the amount is text, like a
// transaction's, and 0 removes the line.
type budgetIn struct {
	Category string `json:"category"`
	Period   string `json:"period,omitempty"`
	Planned  string `json:"planned"`
}

func (s *Server) handleSetBudget(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in budgetIn
	if !s.decode(w, r, &in) {
		return
	}
	sp, err := s.spanFor(in.Period)
	if err != nil {
		s.fail(w, err)
		return
	}
	planned, err := plannedAmount(in.Planned, s.Config().BaseCurrency)
	if err != nil {
		s.fail(w, err)
		return
	}
	// The plan is filed in the base it was typed in and read at the rate on
	// file, so the row never moves when the base or a rate does.
	if err := l.SetBudget(r.Context(), in.Category, sp.key(), planned, s.Config().BaseCurrency); err != nil {
		s.fail(w, err)
		return
	}
	b, err := s.budgets(r.Context(), l, sp)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, b)
}

// plannedAmount reads a budget figure typed as text. Zero is a figure here
// rather than a mistake, since it removes the line.
func plannedAmount(text, currency string) (int64, error) {
	a, err := money.Parse(text, currency)
	if errors.Is(err, money.ErrZero) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return a.Minor, nil
}

// helperIn is one budget helper, spec 5.5: copy the previous period, plan
// from the average or the median of past periods, or scale every line.
type helperIn struct {
	Period   string `json:"period,omitempty"`
	Action   string `json:"action"`
	From     string `json:"from,omitempty"`     // copy: the period to copy, default the one before
	Periods  int    `json:"periods,omitempty"`  // average and median: how many past periods, default 3 and 12
	Percent  int    `json:"percent,omitempty"`  // scale: for example 5 or -10
	Category string `json:"category,omitempty"` // average and median: one category, default every planned one
}

func (s *Server) handleBudgetHelpers(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in helperIn
	if !s.decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	sp, err := s.spanFor(in.Period)
	if err != nil {
		s.fail(w, err)
		return
	}
	var n int
	switch in.Action {
	case "copy":
		from := in.From
		if from == "" {
			from = span{sp.start.AddDate(0, -1, 0)}.key()
		}
		n, err = l.CopyBudget(ctx, from, sp.key())
	case domain.PlanAverage, domain.PlanMedian:
		count := in.Periods
		if count <= 0 {
			count = 3
			if in.Action == domain.PlanMedian {
				count = 12
			}
		}
		if count > 36 {
			count = 36
		}
		past := spansBack(span{sp.start.AddDate(0, -1, 0)}, count)
		ranges := make([][2]string, 0, len(past))
		for _, p := range past {
			ranges = append(ranges, [2]string{p.from(), p.to()})
		}
		n, err = l.PlanFromHistory(ctx, sp.key(), ranges, in.Action, in.Category)
	case "scale":
		n, err = l.ScaleBudget(ctx, sp.key(), in.Percent)
	default:
		err = fmt.Errorf("unknown helper %q: copy, average, median or scale", in.Action)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	b, err := s.budgets(ctx, l, sp)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Changed int `json:"changed"`
		domain.Budget
	}{n, b})
}

// ---- periods, spec 5.4

// span is one budget period: it begins on the configured start day and runs
// to the day before the next one.
type span struct{ start time.Time }

func (s span) end() time.Time { return s.start.AddDate(0, 1, -1) }
func (s span) from() string   { return s.start.Format(dateFmt) }
func (s span) to() string     { return s.end().Format(dateFmt) }
func (s span) days() int      { return int(s.end().Sub(s.start).Hours()/24) + 1 }

// key is what the period is filed under in the budgets table: the year and
// month it begins in.
func (s span) key() string { return s.start.Format("2006-01") }

// label is the three-letter month the period begins in.
func (s span) label() string { return s.start.Month().String()[:3] }

// bounds is the period as the dashboard reports it, with how far into it
// now is.
func (s span) bounds(now time.Time) period {
	return period{
		From:    s.from(),
		To:      s.to(),
		Days:    s.days(),
		Elapsed: int(now.Sub(s.start).Hours()/24) + 1,
	}
}

// periodStart is the first day of the period holding now: the period starts
// on startDay each month, not hardcoded to the 1st, and a date before that
// day belongs to the period that began last month.
func periodStart(now time.Time, startDay int) time.Time {
	if startDay < 1 || startDay > 28 {
		startDay = 1
	}
	y, m, d := now.Date()
	start := time.Date(y, m, startDay, 0, 0, 0, 0, now.Location())
	if d < startDay {
		start = start.AddDate(0, -1, 0)
	}
	return start
}

// periodBounds is the period holding now, and every statistic respects it.
func periodBounds(now time.Time, startDay int) period {
	return span{periodStart(now, startDay)}.bounds(now)
}

// spanForKey is the period filed under a YYYY-MM key: the one beginning on
// startDay of that month.
func spanForKey(key string, startDay int) (span, error) {
	m, err := time.Parse("2006-01", key)
	if err != nil {
		return span{}, fmt.Errorf("period %q must be YYYY-MM", key)
	}
	if startDay < 1 || startDay > 28 {
		startDay = 1
	}
	return span{time.Date(m.Year(), m.Month(), startDay, 0, 0, 0, 0, time.UTC)}, nil
}

// spansBack lists the n periods ending with current, oldest first. Start
// days stop at the 28th, so stepping back a month at a time always lands on
// the same day.
func spansBack(current span, n int) []span {
	out := make([]span, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, span{current.start.AddDate(0, -i, 0)})
	}
	return out
}
