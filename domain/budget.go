package domain

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/money"
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

	lines, err := l.budgetLines(ctx, periodKey)
	if err != nil {
		return Budget{}, err
	}
	planned, err := l.plannedInReference(ctx, lines)
	if err != nil {
		return Budget{}, err
	}
	b := Budget{Period: periodKey, Cards: []BudgetCard{}, Unbudgeted: []BudgetCard{}}
	budgeted := map[string]bool{}
	for _, ln := range lines {
		id := ln.CategoryID
		c, ok := index[id]
		if !ok {
			continue
		}
		budgeted[id] = true
		card := BudgetCard{
			CategoryID: id, Name: c.Name, Icon: index.Icon(id),
			Planned: planned[id], Spent: spent[id], Remaining: planned[id] - spent[id],
			Pct: pct(spent[id], planned[id]),
		}
		card.State = budgetState(card.Spent, card.Planned)
		b.Planned += card.Planned
		b.Spent += card.Spent
		b.Cards = append(b.Cards, card)
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

// budgetLine is one row of the budgets table: the plan as typed, in its own
// currency, and the carry, which is always in the reference.
type budgetLine struct {
	CategoryID string
	Planned    int64
	Currency   string
	RolloverIn int64
}

// budgetLines reads a period's plan. The cursor is drained before returning,
// since the ledger has one connection.
func (l *Ledger) budgetLines(ctx context.Context, periodKey string) ([]budgetLine, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT category_id, planned, currency, rollover_in FROM budgets WHERE period=?`, periodKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []budgetLine
	for rows.Next() {
		var ln budgetLine
		if err := rows.Scan(&ln.CategoryID, &ln.Planned, &ln.Currency, &ln.RolloverIn); err != nil {
			return nil, err
		}
		// A line written without a currency is in the reference.
		if ln.Currency == "" {
			ln.Currency = l.reference
		}
		out = append(out, ln)
	}
	return out, rows.Err()
}

// plannedInReference is each line's plan in the reference at today's rate,
// keyed by category, which is the only form it can be held against spending
// in. A plan in the reference is returned as it is. A currency with no rate
// on file is refused rather than counted as nothing.
func (l *Ledger) plannedInReference(ctx context.Context, lines []budgetLine) (map[string]int64, error) {
	out := make(map[string]int64, len(lines))
	rates := map[string]money.Rate{l.reference: "1"}
	for _, ln := range lines {
		if _, seen := rates[ln.Currency]; !seen {
			rate, ok, err := l.RateOn(ctx, ln.Currency, "")
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("%s is planned in %s and no %s rate is on file", ln.CategoryID, ln.Currency, ln.Currency)
			}
			rates[ln.Currency] = rate
		}
		v, ok := ToReference(ln.Planned, ln.Currency, l.reference, rates)
		if !ok {
			return nil, fmt.Errorf("%s: the %s plan cannot be read in %s", ln.CategoryID, ln.Currency, l.reference)
		}
		out[ln.CategoryID] = v
	}
	return out, nil
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
	lines, err := l.budgetLines(ctx, fromKey)
	if err != nil {
		return 0, err
	}
	for _, ln := range lines {
		if err := l.SetBudget(ctx, ln.CategoryID, toKey, ln.Planned, ln.Currency); err != nil {
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
// category already planned under key. Spending is summed in the reference,
// so the line is filed in it. Categories that spent nothing in any range
// keep their line at zero and are removed.
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
		if err := l.SetBudget(ctx, id, key, planned, l.reference); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ScaleBudget multiplies every line under key by (100+percent)/100, rounded
// half away from zero in the line's own currency, and reports how many lines
// changed.
func (l *Ledger) ScaleBudget(ctx context.Context, key string, percent int) (int, error) {
	if err := checkPeriodKey(key); err != nil {
		return 0, err
	}
	if percent <= -100 || percent > 1000 {
		return 0, fmt.Errorf("scale %d%% is out of range", percent)
	}
	lines, err := l.budgetLines(ctx, key)
	if err != nil {
		return 0, err
	}
	for _, ln := range lines {
		scaled := roundDiv(ln.Planned*int64(100+percent), 100)
		if err := l.SetBudget(ctx, ln.CategoryID, key, scaled, ln.Currency); err != nil {
			return 0, err
		}
	}
	return len(lines), nil
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

// SetBudget files planned minor units of currency for a category under
// periodKey, replacing any earlier figure. An empty currency is the
// reference; any other needs a rate on file, or the plan could not be held
// against spending. Zero removes the line. Only an expense category that is
// neither archived nor a system marker can be budgeted.
func (l *Ledger) SetBudget(ctx context.Context, categoryRef, periodKey string, planned int64, currency string) error {
	if planned < 0 {
		return errors.New("a budget cannot be negative")
	}
	if err := checkPeriodKey(periodKey); err != nil {
		return err
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = l.reference
	}
	if len(currency) != 3 {
		return fmt.Errorf("currency %q must be a three-letter code", currency)
	}
	if _, err := l.SeedFor(ctx, currency); err != nil {
		return err
	}
	if _, ok, err := l.RateOn(ctx, currency, ""); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("no %s rate on file: a plan in %s cannot be held against spending", currency, currency)
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
	_, err = l.db.ExecContext(ctx, `INSERT INTO budgets (id,category_id,period,planned,currency) VALUES (?,?,?,?,?)
		ON CONFLICT(category_id,period) DO UPDATE SET planned=excluded.planned, currency=excluded.currency`,
		newID(), c.ID, periodKey, planned, currency)
	return err
}

// checkPeriodKey accepts the YYYY-MM form budgets are filed under.
func checkPeriodKey(key string) error {
	if _, err := time.Parse("2006-01", key); err != nil {
		return fmt.Errorf("period %q must be YYYY-MM", key)
	}
	return nil
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
