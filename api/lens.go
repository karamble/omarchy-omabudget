package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/money"
)

// lens shows the ledger's reference figures in the base currency for one
// response, at the rate on file as of today, so every figure in a document
// shares one rate. While the base is the reference it changes nothing.
// Ratios pass through untouched: they were computed from reference amounts
// and mean the same in any currency.
type lens struct {
	reference string
	base      string
	// toBase is the exact rate from the reference to the base and toRef the
	// one back; both are empty while the base is the reference.
	toBase money.Rate
	toRef  money.Rate
	// err is the first conversion that failed, checked once a document is
	// built, so a figure too large for the base fails the response rather
	// than showing a wrong number.
	err error
}

// lensFor builds the lens for the configured base. A base was checked for a
// rate when it was set and cannot lose its last one, so a base with no rate
// on file is refused rather than guessed at.
func (s *Server) lensFor(ctx context.Context, l *domain.Ledger) (*lens, error) {
	v := &lens{reference: l.Reference(), base: strings.ToUpper(s.Config().BaseCurrency)}
	if v.base == "" || v.base == v.reference {
		v.base = v.reference
		return v, nil
	}
	rate, ok, err := l.RateOn(ctx, v.base, "")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no %s rate on file: figures cannot be shown in %s", v.base, v.base)
	}
	// The table says what one base unit is in the reference; the reference in
	// the base is its inverse, kept as a fraction so nothing rounds twice.
	if v.toBase, err = money.CrossRate("1", rate); err != nil {
		return nil, err
	}
	v.toRef = rate
	return v, nil
}

// done reports the first conversion that failed while showing a document.
func (v *lens) done() error { return v.err }

// amount is a reference figure in the base.
func (v *lens) amount(minor int64) int64 {
	if v.toBase == "" {
		return minor
	}
	out, err := money.Convert(money.New(minor, v.reference), v.toBase, v.base)
	if err != nil {
		if v.err == nil {
			v.err = fmt.Errorf("showing %s in %s: %w", v.reference, v.base, err)
		}
		return 0
	}
	return out.Minor
}

// toReference is a figure typed in the base as the ledger keeps it.
func (v *lens) toReference(minor int64) (int64, error) {
	if v.toRef == "" {
		return minor, nil
	}
	out, err := money.Convert(money.New(minor, v.base), v.toRef, v.reference)
	if err != nil {
		return 0, err
	}
	return out.Minor, nil
}

func (v *lens) totals(t domain.Totals) domain.Totals {
	t.Income, t.Expense, t.Net = v.amount(t.Income), v.amount(t.Expense), v.amount(t.Net)
	return t
}

func (v *lens) cash(c domain.CashSeries) domain.CashSeries {
	for i := range c.Series {
		c.Series[i].Liquid = v.amount(c.Series[i].Liquid)
	}
	c.Delta = v.amount(c.Delta)
	return c
}

func (v *lens) card(c domain.BudgetCard) domain.BudgetCard {
	c.Planned, c.Spent, c.Remaining = v.amount(c.Planned), v.amount(c.Spent), v.amount(c.Remaining)
	return c
}

func (v *lens) budget(b domain.Budget) domain.Budget {
	b.Planned, b.Spent = v.amount(b.Planned), v.amount(b.Spent)
	for i := range b.Cards {
		b.Cards[i] = v.card(b.Cards[i])
	}
	for i := range b.Unbudgeted {
		b.Unbudgeted[i] = v.card(b.Unbudgeted[i])
	}
	return b
}

func (v *lens) envelopes(e domain.Envelopes) domain.Envelopes {
	e.Liquid, e.RolloverIn, e.Assigned, e.Spent = v.amount(e.Liquid), v.amount(e.RolloverIn), v.amount(e.Assigned), v.amount(e.Spent)
	e.Held, e.Deficit, e.ToBeBudgeted = v.amount(e.Held), v.amount(e.Deficit), v.amount(e.ToBeBudgeted)
	for i := range e.Items {
		it := &e.Items[i]
		it.RolloverIn, it.Assigned, it.Spent, it.Available = v.amount(it.RolloverIn), v.amount(it.Assigned), v.amount(it.Spent), v.amount(it.Available)
		it.GoalTarget, it.Accrual = v.amount(it.GoalTarget), v.amount(it.Accrual)
	}
	return e
}

func (v *lens) spendingRow(r domain.CategorySpend) domain.CategorySpend {
	r.Spent, r.Previous, r.Delta, r.Planned = v.amount(r.Spent), v.amount(r.Previous), v.amount(r.Delta), v.amount(r.Planned)
	for i := range r.Children {
		r.Children[i] = v.spendingRow(r.Children[i])
	}
	return r
}

func (v *lens) spending(r domain.SpendingReport) domain.SpendingReport {
	r.Total, r.Previous = v.amount(r.Total), v.amount(r.Previous)
	for i := range r.Rows {
		r.Rows[i] = v.spendingRow(r.Rows[i])
	}
	return r
}

func (v *lens) metrics(m domain.Metrics) domain.Metrics {
	m.Expense, m.AverageDaily, m.RecurringDue = v.amount(m.Expense), v.amount(m.AverageDaily), v.amount(m.RecurringDue)
	m.Projected, m.Trailing = v.amount(m.Projected), v.amount(m.Trailing)
	return m
}

func (v *lens) categories(cats []domain.Category) []domain.Category {
	for i := range cats {
		cats[i].GoalTarget = v.amount(cats[i].GoalTarget)
	}
	return cats
}

// inReference reads a figure typed in currency as the ledger keeps it, at
// today's rate. The second result is false when no rate is on file for that
// currency.
func inReference(ctx context.Context, l *domain.Ledger, minor int64, currency string) (int64, bool, error) {
	if currency == l.Reference() {
		return minor, true, nil
	}
	rate, ok, err := l.RateOn(ctx, currency, "")
	if err != nil || !ok {
		return 0, false, err
	}
	out, err := money.Convert(money.New(minor, currency), rate, l.Reference())
	if err != nil {
		return 0, false, err
	}
	return out.Minor, true, nil
}

// checkBase validates a base currency as typed: three letters, and either
// the reference or a currency with a rate on file, or figures could not be
// shown in it.
func checkBase(ctx context.Context, l *domain.Ledger, code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 3 || strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return "", fmt.Errorf("base currency %q must be three letters", code)
	}
	if code == l.Reference() {
		return code, nil
	}
	// Choosing a currency to show figures in brings it into use, so it gets
	// its starting rate here like an account would.
	if _, err := l.SeedFor(ctx, code); err != nil {
		return "", err
	}
	_, ok, err := l.RateOn(ctx, code, "")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no %s rate on file: file one before showing figures in %s", code, code)
	}
	return code, nil
}
