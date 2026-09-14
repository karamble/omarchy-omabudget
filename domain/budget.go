package domain

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ---- categories by id

// CategoryIndex is every category keyed by id, for decorating rows.
type CategoryIndex map[string]Category

// CategoryIndex loads the index.
func (l *Ledger) CategoryIndex(ctx context.Context) (CategoryIndex, error) {
	cats, err := l.Categories(ctx)
	if err != nil {
		return nil, err
	}
	idx := make(CategoryIndex, len(cats))
	for _, c := range cats {
		idx[c.ID] = c
	}
	return idx, nil
}

// Name is the category's name, blank for an unknown or empty id.
func (x CategoryIndex) Name(id string) string { return x[id].Name }

// Icon is the category's glyph, or its parent's when it has none of its
// own, which is how the seeded children are drawn.
func (x CategoryIndex) Icon(id string) string {
	c := x[id]
	if c.Icon == "" && c.ParentID != "" {
		return x[c.ParentID].Icon
	}
	return c.Icon
}

// ---- spending, spec 1.3 and 5

// SpendingByCategory sums each category's own expense lines over the
// inclusive range, keyed by category id, as -base_amount. A transaction
// without splits counts under its category; one with splits counts each
// line under the line's category and nothing under its own. A parent's
// entry does not include its children.
func (l *Ledger) SpendingByCategory(ctx context.Context, from, to string) (map[string]int64, error) {
	return l.spendingByCategory(ctx, from, to, false)
}

// spendingByCategory is the same sum, with the option of leaving out the
// categories flagged out of the statistics. A budget counts them, because
// budgeting one is a deliberate act; a report does not, spec 6.
func (l *Ledger) spendingByCategory(ctx context.Context, from, to string, skipFlagged bool) (map[string]int64, error) {
	if _, _, err := parseRange(from, to); err != nil {
		return nil, err
	}
	flagged := ""
	if skipFlagged {
		flagged = ` AND COALESCE(c.exclude_from_statistics,0)=0`
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT category_id, SUM(-base_amount) FROM (
		  SELECT t.category_id AS category_id, t.base_amount AS base_amount FROM transactions t
		    LEFT JOIN categories c ON c.id=t.category_id
		    WHERE t.kind='expense' AND t.deleted_at IS NULL AND t.date>=? AND t.date<=?
		      AND t.category_id IS NOT NULL`+flagged+`
		      AND NOT EXISTS (SELECT 1 FROM splits s WHERE s.transaction_id=t.id)
		  UNION ALL
		  SELECT s.category_id AS category_id, s.base_amount AS base_amount FROM splits s
		    JOIN transactions t ON t.id=s.transaction_id
		    LEFT JOIN categories c ON c.id=s.category_id
		    WHERE t.kind='expense' AND t.deleted_at IS NULL AND t.date>=? AND t.date<=?`+flagged+`
		) GROUP BY category_id`, from, to, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var spent int64
		if err := rows.Scan(&id, &spent); err != nil {
			return nil, err
		}
		out[id] = spent
	}
	return out, rows.Err()
}

// ---- budgets, spec 1.7 and 5

// Budget states.
const (
	BudgetOK   = "ok"
	BudgetNear = "near"
	BudgetOver = "over"
)

// nearLimit is the percentage used at which a card turns near.
const nearLimit = 85

// BudgetCard is one budgeted category against its spending in the period.
type BudgetCard struct {
	CategoryID string `json:"categoryId"`
	Name       string `json:"name"`
	Icon       string `json:"icon"`
	Planned    int64  `json:"planned"`
	Spent      int64  `json:"spent"`
	Remaining  int64  `json:"remaining"` // planned less spent, negative when over
	Pct        int    `json:"pct"`       // spent as a percentage of planned, 0 when nothing is planned
	State      string `json:"state"`
}

// Budget is one period's plan: every budgeted category and their sum.
type Budget struct {
	Period  string       `json:"period"` // the budgets.period key, YYYY-MM
	Planned int64        `json:"planned"`
	Spent   int64        `json:"spent"`
	Pct     int          `json:"pct"`
	Cards   []BudgetCard `json:"cards"`
	// Unbudgeted is where money went that had no plan, largest first.
	Unbudgeted []BudgetCard `json:"unbudgeted"`
}

// Budgets is the plan filed under periodKey against spending over from..to.
// A category's spend is its own plus its direct children's. Cards sort by
// percentage used, highest first, then by name.
func (l *Ledger) Budgets(ctx context.Context, periodKey, from, to string) (Budget, error) {
	if err := checkPeriodKey(periodKey); err != nil {
		return Budget{}, err
	}
	spent, own, index, err := l.spentTree(ctx, from, to, false)
	if err != nil {
		return Budget{}, err
	}

	rows, err := l.db.QueryContext(ctx, `SELECT category_id, planned FROM budgets WHERE period=?`, periodKey)
	if err != nil {
		return Budget{}, err
	}
	defer rows.Close()
	b := Budget{Period: periodKey, Cards: []BudgetCard{}, Unbudgeted: []BudgetCard{}}
	budgeted := map[string]bool{}
	for rows.Next() {
		var id string
		var planned int64
		if err := rows.Scan(&id, &planned); err != nil {
			return Budget{}, err
		}
		c, ok := index[id]
		if !ok {
			continue
		}
		budgeted[id] = true
		card := BudgetCard{
			CategoryID: id, Name: c.Name, Icon: index.Icon(id),
			Planned: planned, Spent: spent[id], Remaining: planned - spent[id],
			Pct: pct(spent[id], planned),
		}
		card.State = budgetState(card.Spent, card.Planned)
		b.Planned += card.Planned
		b.Spent += card.Spent
		b.Cards = append(b.Cards, card)
	}
	if err := rows.Err(); err != nil {
		return Budget{}, err
	}
	b.Pct = pct(b.Spent, b.Planned)
	slices.SortFunc(b.Cards, func(a, c BudgetCard) int {
		return cmp.Or(cmp.Compare(c.Pct, a.Pct), cmp.Compare(a.Name, c.Name))
	})

	// Spending with no plan, by the lines filed on each category itself, so
	// a parent shows only what was booked on it directly. A child under a
	// planned parent is covered by that plan.
	for id, c := range index {
		if budgeted[id] || c.Kind != string(Expense) || c.System || c.Archived || own[id] <= 0 {
			continue
		}
		if c.ParentID != "" && budgeted[c.ParentID] {
			continue
		}
		b.Unbudgeted = append(b.Unbudgeted, BudgetCard{
			CategoryID: id, Name: c.Name, Icon: index.Icon(id), Spent: own[id], Remaining: -own[id], State: BudgetOver,
		})
	}
	slices.SortFunc(b.Unbudgeted, func(a, c BudgetCard) int {
		return cmp.Or(cmp.Compare(c.Spent, a.Spent), cmp.Compare(a.Name, c.Name))
	})
	return b, nil
}

// spentTree is spending per category over the range with each parent
// carrying its children's lines too, next to each category's own lines.
func (l *Ledger) spentTree(ctx context.Context, from, to string, skipFlagged bool) (map[string]int64, map[string]int64, CategoryIndex, error) {
	spending, err := l.spendingByCategory(ctx, from, to, skipFlagged)
	if err != nil {
		return nil, nil, nil, err
	}
	index, err := l.CategoryIndex(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	spent := map[string]int64{}
	for id, c := range index {
		spent[id] += spending[id]
		if c.ParentID != "" {
			spent[c.ParentID] += spending[id]
		}
	}
	return spent, spending, index, nil
}

// ---- budget helpers, spec 5.5

// CopyBudget files the plan under fromKey again under toKey, line for line,
// and reports how many lines it wrote.
func (l *Ledger) CopyBudget(ctx context.Context, fromKey, toKey string) (int, error) {
	if err := checkPeriodKey(fromKey); err != nil {
		return 0, err
	}
	if err := checkPeriodKey(toKey); err != nil {
		return 0, err
	}
	if fromKey == toKey {
		return 0, errors.New("copying a period onto itself changes nothing")
	}
	rows, err := l.db.QueryContext(ctx, `SELECT category_id, planned FROM budgets WHERE period=?`, fromKey)
	if err != nil {
		return 0, err
	}
	type line struct {
		id      string
		planned int64
	}
	var lines []line
	for rows.Next() {
		var ln line
		if err := rows.Scan(&ln.id, &ln.planned); err != nil {
			rows.Close()
			return 0, err
		}
		lines = append(lines, ln)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, ln := range lines {
		if err := l.SetBudget(ctx, ln.id, toKey, ln.planned); err != nil {
			return 0, err
		}
	}
	return len(lines), nil
}

// Plan methods for PlanFromHistory.
const (
	PlanAverage = "average"
	PlanMedian  = "median"
)

// PlanFromHistory sets the plan under key to the average or the median of
// what was spent over the given past ranges, for one category or for every
// category already planned under key. Categories that spent nothing in any
// range keep their line at zero and are removed.
func (l *Ledger) PlanFromHistory(ctx context.Context, key string, ranges [][2]string, method, categoryRef string) (int, error) {
	if err := checkPeriodKey(key); err != nil {
		return 0, err
	}
	if method != PlanAverage && method != PlanMedian {
		return 0, fmt.Errorf("unknown method %q: average or median", method)
	}
	if len(ranges) == 0 {
		return 0, errors.New("no past periods to look at")
	}
	var targets []string
	if categoryRef != "" {
		c, err := l.Category(ctx, categoryRef)
		if err != nil {
			return 0, err
		}
		targets = []string{c.ID}
	} else {
		rows, err := l.db.QueryContext(ctx, `SELECT category_id FROM budgets WHERE period=?`, key)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			targets = append(targets, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
	}
	history := make([]map[string]int64, 0, len(ranges))
	for _, r := range ranges {
		spent, _, _, err := l.spentTree(ctx, r[0], r[1], false)
		if err != nil {
			return 0, err
		}
		history = append(history, spent)
	}
	n := 0
	for _, id := range targets {
		values := make([]int64, 0, len(history))
		for _, h := range history {
			values = append(values, h[id])
		}
		var planned int64
		if method == PlanAverage {
			var sum int64
			for _, v := range values {
				sum += v
			}
			planned = roundDiv(sum, int64(len(values)))
		} else {
			slices.Sort(values)
			mid := len(values) / 2
			if len(values)%2 == 1 {
				planned = values[mid]
			} else {
				planned = roundDiv(values[mid-1]+values[mid], 2)
			}
		}
		if err := l.SetBudget(ctx, id, key, planned); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ScaleBudget multiplies every line under key by (100+percent)/100, rounded
// half away from zero, and reports how many lines changed.
func (l *Ledger) ScaleBudget(ctx context.Context, key string, percent int) (int, error) {
	if err := checkPeriodKey(key); err != nil {
		return 0, err
	}
	if percent <= -100 || percent > 1000 {
		return 0, fmt.Errorf("scale %d%% is out of range", percent)
	}
	rows, err := l.db.QueryContext(ctx, `SELECT category_id, planned FROM budgets WHERE period=?`, key)
	if err != nil {
		return 0, err
	}
	changes := map[string]int64{}
	for rows.Next() {
		var id string
		var planned int64
		if err := rows.Scan(&id, &planned); err != nil {
			rows.Close()
			return 0, err
		}
		changes[id] = roundDiv(planned*int64(100+percent), 100)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for id, planned := range changes {
		if err := l.SetBudget(ctx, id, key, planned); err != nil {
			return 0, err
		}
	}
	return len(changes), nil
}

// roundDiv divides rounding half away from zero.
func roundDiv(a, b int64) int64 {
	if b == 0 {
		return 0
	}
	q, r := a/b, a%b
	if r < 0 {
		r = -r
	}
	if 2*r >= b {
		if a < 0 {
			return q - 1
		}
		return q + 1
	}
	return q
}

// SetBudget files planned base-currency minor units for a category under
// periodKey, replacing any earlier figure. Zero removes the line. Only an
// expense category that is neither archived nor a system marker can be
// budgeted.
func (l *Ledger) SetBudget(ctx context.Context, categoryRef, periodKey string, planned int64) error {
	if planned < 0 {
		return errors.New("a budget cannot be negative")
	}
	if err := checkPeriodKey(periodKey); err != nil {
		return err
	}
	c, err := l.Category(ctx, categoryRef)
	if err != nil {
		return err
	}
	if c.Kind != string(Expense) {
		return fmt.Errorf("%s is an %s category and cannot be budgeted", c.Name, c.Kind)
	}
	if c.Archived {
		return fmt.Errorf("%s is archived", c.Name)
	}
	if c.System {
		return fmt.Errorf("%s is a system category and cannot be budgeted", c.Name)
	}
	if planned == 0 {
		// A line that carries a rollover stays, at zero, so the carry is kept.
		if _, err := l.db.ExecContext(ctx, `DELETE FROM budgets WHERE category_id=? AND period=? AND rollover_in=0`, c.ID, periodKey); err != nil {
			return err
		}
		_, err := l.db.ExecContext(ctx, `UPDATE budgets SET planned=0 WHERE category_id=? AND period=?`, c.ID, periodKey)
		return err
	}
	_, err = l.db.ExecContext(ctx, `INSERT INTO budgets (id,category_id,period,planned) VALUES (?,?,?,?)
		ON CONFLICT(category_id,period) DO UPDATE SET planned=excluded.planned`,
		newID(), c.ID, periodKey, planned)
	return err
}

// checkPeriodKey accepts the YYYY-MM form budgets are filed under.
func checkPeriodKey(key string) error {
	if _, err := time.Parse("2006-01", key); err != nil {
		return fmt.Errorf("period %q must be YYYY-MM", key)
	}
	return nil
}

// pct is spent as a whole percentage of planned, 0 when nothing is planned.
func pct(spent, planned int64) int {
	if planned <= 0 {
		return 0
	}
	return int(spent * 100 / planned)
}

func budgetState(spent, planned int64) string {
	switch {
	case spent > planned:
		return BudgetOver
	case pct(spent, planned) >= nearLimit:
		return BudgetNear
	}
	return BudgetOK
}
