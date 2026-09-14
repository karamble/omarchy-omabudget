package api

import (
	"context"
	"time"

	"github.com/karamble/omarchy-omabudget/alerts"
	"github.com/karamble/omarchy-omabudget/domain"
)

// Snapshot is what triggers are evaluated against: the period's budget
// cards, what is due, large postings and low accounts, laid out under the
// catalogue's paths. An unreadable ledger yields an empty snapshot rather
// than an error, so a trigger simply does not fire.
func (s *Server) Snapshot() alerts.Snapshot {
	snap := alerts.Snapshot{
		Numbers:    map[string]float64{},
		Texts:      map[string]string{},
		Lists:      map[string][]map[string]any{},
		Monitoring: s.Config().MonitoringOn(),
		TakenAt:    clock(),
	}
	l := s.Ledger()
	if l == nil {
		return snap
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := s.Config()
	now := clock()
	current := span{periodStart(now, cfg.PeriodStartDay)}

	if b, err := l.Budgets(ctx, current.key(), current.from(), current.to()); err == nil {
		var warn, over []map[string]any
		for _, c := range b.Cards {
			row := map[string]any{
				"category": c.Name, "planned": c.Planned, "actual": c.Spent,
				"remaining": c.Remaining, "utilisation": c.Pct,
			}
			if c.Pct >= 100 {
				over = append(over, row)
			}
			if c.Pct >= 80 {
				warn = append(warn, row)
			}
		}
		snap.Lists["budget.warn"] = warn
		snap.Lists["budget.over"] = over
		snap.Numbers["budget.categoriesAtWarn"] = float64(len(warn))
		snap.Numbers["budget.categoriesOver"] = float64(len(over))
	}
	if tot, err := l.Totals(ctx, current.from(), current.to()); err == nil {
		snap.Numbers["period.spent"] = float64(tot.Expense)
		snap.Numbers["period.savingsRate"] = float64(tot.SavingsRate)
	}

	today := now.Format(dateFmt)
	if due, err := l.Upcoming(ctx, today, now.AddDate(0, 0, 60).Format(dateFmt)); err == nil {
		rules := map[string]domain.Rule{}
		if list, err := l.Rules(ctx, false); err == nil {
			for _, r := range list {
				rules[r.ID] = r
			}
		}
		var soon, overdue []map[string]any
		for _, d := range due {
			row := map[string]any{
				"id": d.RuleID + "@" + d.Date, "name": d.Name, "amount": d.Amount,
				"category": d.CategoryID, "account": d.AccountID, "due": d.Date, "daysLeft": d.DaysUntil,
			}
			switch {
			case d.Overdue:
				overdue = append(overdue, row)
			case d.DaysUntil <= rules[d.RuleID].LeadDays:
				soon = append(soon, row)
			}
		}
		snap.Lists["bills.due"] = soon
		snap.Lists["bills.overdue"] = overdue
		snap.Numbers["bills.dueCount"] = float64(len(soon))
		snap.Numbers["bills.overdueCount"] = float64(len(overdue))
	}

	if cfg.LargeAmount > 0 {
		if list, err := l.Transactions(ctx, domain.Filter{From: now.AddDate(0, 0, -30).Format(dateFmt), Limit: 500}); err == nil {
			var large []map[string]any
			for _, t := range list {
				a := t.BaseAmount
				if a < 0 {
					a = -a
				}
				if a < cfg.LargeAmount {
					continue
				}
				large = append(large, map[string]any{
					"id": t.ID, "date": t.Date, "description": t.Description, "amount": t.BaseAmount,
					"category": t.CategoryID, "account": t.AccountID, "payee": t.PayeeID,
				})
			}
			snap.Lists["ledger.large"] = large
		}
	}

	if accounts, err := l.Accounts(ctx); err == nil {
		var low []map[string]any
		for _, a := range accounts {
			if !a.Active || a.LowBalance == nil {
				continue
			}
			b, err := l.Balance(ctx, a.ID)
			if err != nil || b.Minor >= *a.LowBalance {
				continue
			}
			low = append(low, map[string]any{
				"account": a.Name, "balance": b.Minor, "threshold": *a.LowBalance, "currency": a.Currency,
			})
		}
		snap.Lists["accounts.low"] = low
	}
	if snap.Monitoring {
		snap.Texts["health.monitoring"] = "true"
	} else {
		snap.Texts["health.monitoring"] = "false"
	}
	return snap
}
