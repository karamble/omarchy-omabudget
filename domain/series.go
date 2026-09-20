package domain

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const dateFmt = "2006-01-02"

// ---- totals, spec 6.1

// Totals is income against expense over a date range, on base amounts.
// Transfers never count, and neither does a category flagged out of
// statistics.
type Totals struct {
	Income      int64 `json:"income"`
	Expense     int64 `json:"expense"`
	Net         int64 `json:"net"`
	SavingsRate int   `json:"savingsRate"` // percent, 0 when no income
}

// Totals sums the inclusive date range.
func (l *Ledger) Totals(ctx context.Context, from, to string) (Totals, error) {
	if _, _, err := parseRange(from, to); err != nil {
		return Totals{}, err
	}
	var t Totals
	// A transaction booked to one category counts whole; a split counts line
	// by line, because that is where its categories are. Either way a
	// category flagged out of the statistics is left out, spec 6.
	err := l.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN kind='income' THEN amount ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN kind='expense' THEN -amount ELSE 0 END),0)
		FROM (
		  SELECT t.kind AS kind, t.base_amount AS amount
		    FROM transactions t LEFT JOIN categories c ON c.id=t.category_id
		    WHERE t.deleted_at IS NULL AND t.date>=? AND t.date<=?
		      AND COALESCE(c.exclude_from_statistics,0)=0
		      AND NOT EXISTS (SELECT 1 FROM splits s WHERE s.transaction_id=t.id)
		  UNION ALL
		  SELECT t.kind AS kind, s.base_amount AS amount
		    FROM splits s
		    JOIN transactions t ON t.id=s.transaction_id
		    LEFT JOIN categories c ON c.id=s.category_id
		    WHERE t.deleted_at IS NULL AND t.date>=? AND t.date<=?
		      AND COALESCE(c.exclude_from_statistics,0)=0
		)`, from, to, from, to).Scan(&t.Income, &t.Expense)
	if err != nil {
		return Totals{}, err
	}
	t.Net = t.Income - t.Expense
	t.SavingsRate = pct(t.Net, t.Income)
	return t, nil
}

// ---- liquid funds by day, spec 6.1

// CashPoint is liquid funds at the end of one day.
type CashPoint struct {
	Date   string `json:"date"`
	Liquid int64  `json:"liquid"`
}

// CashSeries is the daily liquid series over a span of days and how far it
// moved since the day before the span began.
type CashSeries struct {
	Series []CashPoint `json:"series"`
	Delta  int64       `json:"delta"`
	// DeltaPct is Delta as a percentage of the earlier figure's size, to one
	// decimal, so it carries Delta's sign even from an overdraft. Nil when
	// that figure is zero.
	DeltaPct *float64 `json:"deltaPct,omitempty"`
}

// LiquidSeries is end-of-day liquid funds for every day from from to to
// inclusive: liquid accounts in the reference currency, each opening balance
// counted from its opening date, every posting dated on or before the day.
// Balance is undated, so the last point is what the accounts hold less
// anything dated after to.
func (l *Ledger) LiquidSeries(ctx context.Context, from, to string) (CashSeries, error) {
	first, last, err := parseRange(from, to)
	if err != nil {
		return CashSeries{}, err
	}
	accounts, err := l.Accounts(ctx)
	if err != nil {
		return CashSeries{}, err
	}
	// One rate per currency for the whole series, the latest on file: the line
	// is meant to show cash moving, not the rate moving under it.
	rates, err := l.RateTable(ctx, to)
	if err != nil {
		return CashSeries{}, err
	}
	liquid := map[string]bool{}
	currency := map[string]string{}
	byDay := map[string]int64{}
	var ids []any
	for _, a := range accounts {
		if !a.Type.Liquid() {
			continue
		}
		if _, ok := rates[a.Currency]; !ok {
			continue // no rate on file: counted nowhere rather than at par
		}
		liquid[a.ID] = true
		currency[a.ID] = a.Currency
		ids = append(ids, a.ID)
		if v, ok := ToReference(a.OpeningBalance, a.Currency, l.reference, rates); ok {
			byDay[a.OpeningDate] += v
		}
	}
	if len(ids) > 0 {
		in := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := append(append([]any{to}, ids...), ids...)
		rows, err := l.db.QueryContext(ctx, `
			SELECT date, account_id, COALESCE(counter_account_id,''), amount, counter_amount
			FROM transactions
			WHERE deleted_at IS NULL AND date<=?
			  AND (account_id IN (`+in+`) OR counter_account_id IN (`+in+`))
			ORDER BY date`, args...)
		if err != nil {
			return CashSeries{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var date, account, counter string
			var amount int64
			var counterAmount sql.NullInt64
			if err := rows.Scan(&date, &account, &counter, &amount, &counterAmount); err != nil {
				return CashSeries{}, err
			}
			if liquid[account] {
				if v, ok := ToReference(amount, currency[account], l.reference, rates); ok {
					byDay[date] += v
				}
			}
			if liquid[counter] {
				leg := -amount
				if counterAmount.Valid {
					leg = counterAmount.Int64
				}
				if v, ok := ToReference(leg, currency[counter], l.reference, rates); ok {
					byDay[date] += v
				}
			}
		}
		if err := rows.Err(); err != nil {
			return CashSeries{}, err
		}
	}

	var before int64
	for date, v := range byDay {
		if date < from {
			before += v
		}
	}
	running := before
	c := CashSeries{Series: make([]CashPoint, 0, int(last.Sub(first).Hours()/24)+1)}
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		date := d.Format(dateFmt)
		running += byDay[date]
		c.Series = append(c.Series, CashPoint{Date: date, Liquid: running})
	}
	c.Delta = running - before
	c.DeltaPct = deltaPct(c.Delta, before)
	return c, nil
}

// parseRange checks an inclusive date range.
func parseRange(from, to string) (time.Time, time.Time, error) {
	first, err := time.Parse(dateFmt, from)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from %q must be YYYY-MM-DD", from)
	}
	last, err := time.Parse(dateFmt, to)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to %q must be YYYY-MM-DD", to)
	}
	if last.Before(first) {
		return time.Time{}, time.Time{}, fmt.Errorf("range ends on %s, before it starts on %s", to, from)
	}
	return first, last, nil
}
