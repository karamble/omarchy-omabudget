package domain

import (
	"cmp"
	"context"
	"slices"
	"time"
)

// CategorySpend is one line of the spending report, spec R1: a category
// against the window before and against its plan.
type CategorySpend struct {
	CategoryID string          `json:"categoryId"`
	Name       string          `json:"name"`
	Icon       string          `json:"icon"`
	Spent      int64           `json:"spent"`
	Previous   int64           `json:"previous"`
	Share      int             `json:"share"` // percent of the window's total
	Delta      int64           `json:"delta"`
	DeltaPct   *float64        `json:"deltaPct,omitempty"`
	Planned    int64           `json:"planned,omitempty"`
	Children   []CategorySpend `json:"children,omitempty"`
}

// SpendingReport ranks top-level categories over a window, each with the
// children it was spent through. Categories flagged out of the statistics are
// left out, so it adds up to the same expense the headline reports.
type SpendingReport struct {
	From     string          `json:"from"`
	To       string          `json:"to"`
	PrevFrom string          `json:"prevFrom"`
	PrevTo   string          `json:"prevTo"`
	Total    int64           `json:"total"`
	Previous int64           `json:"previous"`
	Rows     []CategorySpend `json:"rows"`
}

// Spending builds the report for from..to against prevFrom..prevTo. When
// budgetKey names a period, each line carries its plan.
func (l *Ledger) Spending(ctx context.Context, from, to, prevFrom, prevTo, budgetKey string) (SpendingReport, error) {
	spent, own, index, err := l.spentTree(ctx, from, to, true)
	if err != nil {
		return SpendingReport{}, err
	}
	prev, prevOwn, _, err := l.spentTree(ctx, prevFrom, prevTo, true)
	if err != nil {
		return SpendingReport{}, err
	}
	planned := map[string]int64{}
	if budgetKey != "" {
		lines, err := l.budgetLines(ctx, budgetKey)
		if err != nil {
			return SpendingReport{}, err
		}
		if planned, err = l.plannedInReference(ctx, lines); err != nil {
			return SpendingReport{}, err
		}
	}

	out := SpendingReport{From: from, To: to, PrevFrom: prevFrom, PrevTo: prevTo, Rows: []CategorySpend{}}
	for id, c := range index {
		if c.Kind != string(Expense) || c.ParentID != "" {
			continue
		}
		if spent[id] == 0 && prev[id] == 0 {
			continue
		}
		out.Total += spent[id]
		out.Previous += prev[id]
	}
	line := func(id string, s, p int64) CategorySpend {
		c := index[id]
		return CategorySpend{
			CategoryID: id, Name: c.Name, Icon: index.Icon(id), Spent: s, Previous: p,
			Share: pct(s, out.Total), Delta: s - p, DeltaPct: deltaPct(s-p, p), Planned: planned[id],
		}
	}
	for id, c := range index {
		if c.Kind != string(Expense) || c.ParentID != "" || (spent[id] == 0 && prev[id] == 0) {
			continue
		}
		row := line(id, spent[id], prev[id])
		for cid, cc := range index {
			if cc.ParentID != id || (own[cid] == 0 && prevOwn[cid] == 0) {
				continue
			}
			row.Children = append(row.Children, line(cid, own[cid], prevOwn[cid]))
		}
		slices.SortFunc(row.Children, func(a, b CategorySpend) int {
			return cmp.Or(cmp.Compare(b.Spent, a.Spent), cmp.Compare(a.Name, b.Name))
		})
		out.Rows = append(out.Rows, row)
	}
	slices.SortFunc(out.Rows, func(a, b CategorySpend) int {
		return cmp.Or(cmp.Compare(b.Spent, a.Spent), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// Metrics are the headline figures of spec 6.1 that need more than the
// period's totals.
type Metrics struct {
	Expense      int64   `json:"expense"`
	AverageDaily int64   `json:"averageDaily"` // expense per elapsed day
	RecurringDue int64   `json:"recurringDue"` // recurring expense still to fall in the period
	Projected    int64   `json:"projected"`    // average daily over the whole period, plus what is still due
	Trailing     int64   `json:"trailing"`     // average expense of the full periods before
	RunwayMonths float64 `json:"runwayMonths"` // liquid funds over the trailing average
	FixedShare   int     `json:"fixedShare"`   // percent of expense posted by recurring rules
}

// Metrics reads the period from..to with elapsed of days gone, today for
// what is still due, and the trailing full periods for the runway.
func (l *Ledger) Metrics(ctx context.Context, from, to string, days, elapsed int, today string, trailing [][2]string) (Metrics, error) {
	tot, err := l.Totals(ctx, from, to)
	if err != nil {
		return Metrics{}, err
	}
	m := Metrics{Expense: tot.Expense}
	if elapsed > 0 {
		m.AverageDaily = roundDiv(tot.Expense, int64(elapsed))
	}
	due, err := l.Upcoming(ctx, today, to)
	if err != nil {
		return Metrics{}, err
	}
	// A bill in another currency is read at the rate on file, like a
	// balance; one whose currency has no rate cannot be counted.
	rates, err := l.RateTable(ctx, today)
	if err != nil {
		return Metrics{}, err
	}
	for _, d := range due {
		if d.Kind != Expense || d.Date <= today {
			continue
		}
		if v, ok := ToReference(d.Amount, d.Currency, l.reference, rates); ok {
			m.RecurringDue += v
		}
	}
	m.Projected = m.AverageDaily*int64(days) + m.RecurringDue

	if len(trailing) > 0 {
		var sum int64
		for _, r := range trailing {
			t, err := l.Totals(ctx, r[0], r[1])
			if err != nil {
				return Metrics{}, err
			}
			sum += t.Expense
		}
		m.Trailing = roundDiv(sum, int64(len(trailing)))
	}
	if m.Trailing > 0 {
		liquid, _, err := l.Liquid(ctx)
		if err != nil {
			return Metrics{}, err
		}
		m.RunwayMonths = float64(liquid*10/m.Trailing) / 10
	}

	var fixed int64
	err = l.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(-base_amount),0) FROM transactions
		WHERE kind='expense' AND deleted_at IS NULL AND recurring_rule_id IS NOT NULL AND date>=? AND date<=?`, from, to).Scan(&fixed)
	if err != nil {
		return Metrics{}, err
	}
	m.FixedShare = pct(fixed, tot.Expense)
	return m, nil
}

// dateOnly trims a stamp to its day.
func dateOnly(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format(dateFmt)
	}
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
