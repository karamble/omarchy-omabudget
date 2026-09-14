package domain

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Behaviours a category can have under the envelope model, spec 5.2.
const (
	BehaviourMonthly   = "monthly"   // the pot resets each period
	BehaviourRollover  = "rollover"  // what is left, or overspent, carries over
	BehaviourGoal      = "goal"      // carries over and saves toward a target by a month
	BehaviourUntracked = "untracked" // never budgeted
)

var behaviours = map[string]bool{
	BehaviourMonthly: true, BehaviourRollover: true, BehaviourGoal: true, BehaviourUntracked: true,
}

// Envelope is one category's pot in a period: what rolled in, what was
// assigned, what was spent, and what is available.
type Envelope struct {
	CategoryID string `json:"categoryId"`
	Name       string `json:"name"`
	Icon       string `json:"icon"`
	Behaviour  string `json:"behaviour"`
	RolloverIn int64  `json:"rolloverIn"`
	Assigned   int64  `json:"assigned"`
	Spent      int64  `json:"spent"`
	Available  int64  `json:"available"` // rolloverIn plus assigned less spent
	GoalTarget int64  `json:"goalTarget,omitempty"`
	GoalDue    string `json:"goalDue,omitempty"`
	MonthsLeft int    `json:"monthsLeft,omitempty"`
	// Accrual is what assigning this period would keep a goal on track.
	Accrual int64 `json:"accrual,omitempty"`
}

// Envelopes is a period under the envelope model. Held is the money sitting
// in pots, Deficit what the overspent pots are short, and ToBeBudgeted the
// liquid funds not yet in any pot.
type Envelopes struct {
	Period       string     `json:"period"`
	Liquid       int64      `json:"liquid"`
	RolloverIn   int64      `json:"rolloverIn"`
	Assigned     int64      `json:"assigned"`
	Spent        int64      `json:"spent"`
	Held         int64      `json:"held"`
	Deficit      int64      `json:"deficit"`
	ToBeBudgeted int64      `json:"toBeBudgeted"`
	Items        []Envelope `json:"items"`
}

// Liquid sums the balances of the liquid accounts in base currency, reading
// the ones kept in another at the latest rate on file. Currencies with no
// rate are left out and named, so a figure is never quietly short.
func (l *Ledger) Liquid(ctx context.Context) (int64, []string, error) {
	accounts, err := l.Accounts(ctx)
	if err != nil {
		return 0, nil, err
	}
	rates, err := l.RateTable(ctx, "")
	if err != nil {
		return 0, nil, err
	}
	var sum int64
	missing := map[string]bool{}
	for _, a := range accounts {
		if !a.Type.Liquid() {
			continue
		}
		b, err := l.Balance(ctx, a.ID)
		if err != nil {
			return 0, nil, err
		}
		v, ok := InBase(b.Minor, a.Currency, l.base, rates)
		if !ok {
			missing[a.Currency] = true
			continue
		}
		sum += v
	}
	return sum, sortedKeys(missing), nil
}

// sortedKeys is the currencies a figure had to leave out, in a stable order.
func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Envelopes reads the period filed under key against spending over from..to.
func (l *Ledger) Envelopes(ctx context.Context, key, from, to string) (Envelopes, error) {
	if err := checkPeriodKey(key); err != nil {
		return Envelopes{}, err
	}
	spent, _, index, err := l.spentTree(ctx, from, to, false)
	if err != nil {
		return Envelopes{}, err
	}
	liquid, _, err := l.Liquid(ctx)
	if err != nil {
		return Envelopes{}, err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT category_id, planned, rollover_in FROM budgets WHERE period=?`, key)
	if err != nil {
		return Envelopes{}, err
	}
	defer rows.Close()
	out := Envelopes{Period: key, Liquid: liquid, Items: []Envelope{}}
	for rows.Next() {
		var id string
		var assigned, rolled int64
		if err := rows.Scan(&id, &assigned, &rolled); err != nil {
			return Envelopes{}, err
		}
		c, ok := index[id]
		if !ok {
			continue
		}
		e := Envelope{
			CategoryID: id, Name: c.Name, Icon: index.Icon(id), Behaviour: c.Behaviour,
			RolloverIn: rolled, Assigned: assigned, Spent: spent[id],
			Available:  rolled + assigned - spent[id],
			GoalTarget: c.GoalTarget, GoalDue: c.GoalDue,
		}
		if c.Behaviour == BehaviourGoal && c.GoalTarget > 0 {
			e.MonthsLeft = monthsUntil(key, c.GoalDue)
			if e.MonthsLeft > 0 {
				if need := c.GoalTarget - e.Available; need > 0 {
					e.Accrual = roundDiv(need, int64(e.MonthsLeft))
				}
			}
		}
		out.RolloverIn += e.RolloverIn
		out.Assigned += e.Assigned
		out.Spent += e.Spent
		if e.Available > 0 {
			out.Held += e.Available
		} else {
			out.Deficit -= e.Available
		}
		out.Items = append(out.Items, e)
	}
	if err := rows.Err(); err != nil {
		return Envelopes{}, err
	}
	out.ToBeBudgeted = liquid - out.Held
	slices.SortFunc(out.Items, func(a, b Envelope) int {
		return cmp.Or(cmp.Compare(a.Available, b.Available), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// Rollover closes the period under fromKey (spending over from..to) into
// toKey: pots that roll over carry what is left or what is short, monthly
// pots start again at nothing, untracked categories are left alone. It
// reports how many lines it wrote.
func (l *Ledger) Rollover(ctx context.Context, fromKey, from, to, toKey string) (int, error) {
	if err := checkPeriodKey(fromKey); err != nil {
		return 0, err
	}
	if err := checkPeriodKey(toKey); err != nil {
		return 0, err
	}
	if fromKey >= toKey {
		return 0, errors.New("a period rolls over into a later one")
	}
	spent, _, index, err := l.spentTree(ctx, from, to, false)
	if err != nil {
		return 0, err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT category_id, planned, rollover_in FROM budgets WHERE period=?`, fromKey)
	if err != nil {
		return 0, err
	}
	type carry struct {
		id      string
		balance int64
		keep    bool
	}
	var carries []carry
	for rows.Next() {
		var id string
		var assigned, rolled int64
		if err := rows.Scan(&id, &assigned, &rolled); err != nil {
			rows.Close()
			return 0, err
		}
		c, ok := index[id]
		if !ok || c.Behaviour == BehaviourUntracked {
			continue
		}
		keep := c.Behaviour == BehaviourRollover || c.Behaviour == BehaviourGoal
		carries = append(carries, carry{id: id, balance: rolled + assigned - spent[id], keep: keep})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, c := range carries {
		if !c.keep {
			// A monthly pot starts clean; only an existing line needs resetting.
			if _, err := l.db.ExecContext(ctx, `UPDATE budgets SET rollover_in=0 WHERE category_id=? AND period=?`, c.id, toKey); err != nil {
				return n, err
			}
			continue
		}
		if _, err := l.db.ExecContext(ctx, `INSERT INTO budgets (id,category_id,period,planned,rollover_enabled,rollover_in)
			VALUES (?,?,?,0,1,?)
			ON CONFLICT(category_id,period) DO UPDATE SET rollover_in=excluded.rollover_in, rollover_enabled=1`,
			newID(), c.id, toKey, c.balance); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// SetCategoryBehaviour changes how a category's pot behaves.
func (l *Ledger) SetCategoryBehaviour(ctx context.Context, ref, behaviour string) (Category, error) {
	if !behaviours[behaviour] {
		return Category{}, fmt.Errorf("unknown behaviour %q: monthly, rollover, goal or untracked", behaviour)
	}
	c, err := l.Category(ctx, ref)
	if err != nil {
		return Category{}, err
	}
	if c.System {
		return Category{}, fmt.Errorf("%s is a system category", c.Name)
	}
	if behaviour == BehaviourGoal && c.GoalTarget <= 0 {
		return Category{}, fmt.Errorf("%s has no goal yet: set the target and the month first", c.Name)
	}
	if _, err := l.db.ExecContext(ctx, `UPDATE categories SET default_budget_behaviour=? WHERE id=?`, behaviour, c.ID); err != nil {
		return Category{}, err
	}
	c.Behaviour = behaviour
	return c, nil
}

// SetCategoryGoal sets a target to save by the month due (YYYY-MM), which
// makes the category a goal; a target of zero clears the goal and the pot
// keeps rolling over.
func (l *Ledger) SetCategoryGoal(ctx context.Context, ref string, target int64, due string) (Category, error) {
	if target < 0 {
		return Category{}, errors.New("a goal cannot be negative")
	}
	c, err := l.Category(ctx, ref)
	if err != nil {
		return Category{}, err
	}
	if c.System || c.Kind != string(Expense) {
		return Category{}, fmt.Errorf("%s cannot carry a goal", c.Name)
	}
	behaviour := c.Behaviour
	if target == 0 {
		due = ""
		if behaviour == BehaviourGoal {
			behaviour = BehaviourRollover
		}
	} else {
		if err := checkPeriodKey(due); err != nil {
			return Category{}, fmt.Errorf("a goal needs the month it is due, YYYY-MM")
		}
		behaviour = BehaviourGoal
	}
	if _, err := l.db.ExecContext(ctx, `UPDATE categories SET goal_target=?, goal_due=?, default_budget_behaviour=? WHERE id=?`,
		target, due, behaviour, c.ID); err != nil {
		return Category{}, err
	}
	c.GoalTarget, c.GoalDue, c.Behaviour = target, due, behaviour
	return c, nil
}

// monthsUntil counts the periods from key through due inclusive, 0 when due
// has passed or is unreadable.
func monthsUntil(key, due string) int {
	k, err := time.Parse("2006-01", key)
	if err != nil {
		return 0
	}
	d, err := time.Parse("2006-01", due)
	if err != nil {
		return 0
	}
	n := (d.Year()-k.Year())*12 + int(d.Month()) - int(k.Month()) + 1
	if n < 0 {
		return 0
	}
	return n
}
