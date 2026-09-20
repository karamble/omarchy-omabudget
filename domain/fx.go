package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/money"
)

// The rate table, spec 8. Every rate is quoted against one reference
// currency, recorded in the ledger when it was first opened and never moved,
// so a rate means the same thing whatever currency figures are shown in. A
// transaction either follows this table or carries a rate of its own; the
// table is what a balance in another currency is read at.

// FXRate is one day's rate for a currency against the reference. Rederived
// counts the transactions a change to it moved.
type FXRate struct {
	Date      string     `json:"date"`
	Currency  string     `json:"currency"`
	Rate      money.Rate `json:"rate"`
	Rederived int        `json:"rederived,omitempty"`
}

// SetRate files a rate for a currency on a date, replacing that day's if it
// has one. The date defaults to today. Every transaction in that currency
// that follows the table is re-derived in the same transaction; a row that
// carries a rate entered by hand is left alone.
func (l *Ledger) SetRate(ctx context.Context, currency string, rate money.Rate, date string) (FXRate, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return FXRate{}, fmt.Errorf("currency %q must be a three-letter code", currency)
	}
	if currency == l.reference {
		return FXRate{}, fmt.Errorf("%s is the rate reference: its rate is always 1", currency)
	}
	if _, ok := new(big.Rat).SetString(string(rate)); !ok {
		return FXRate{}, fmt.Errorf("rate %q is not a number", rate)
	}
	// Convert proves the rate is usable and positive, which is the only shape
	// a reference amount can be derived from.
	if _, err := money.Convert(money.New(100, currency), rate, l.reference); err != nil {
		return FXRate{}, err
	}
	if date == "" {
		date = l.now().Format(dateFmt)
	}
	if _, err := time.Parse(dateFmt, date); err != nil {
		return FXRate{}, fmt.Errorf("date %q must be YYYY-MM-DD", date)
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return FXRate{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO fx_rates (date,currency,rate) VALUES (?,?,?)
		ON CONFLICT(date,currency) DO UPDATE SET rate=excluded.rate`, date, currency, string(rate)); err != nil {
		return FXRate{}, err
	}
	n, err := l.rederive(ctx, tx, currency)
	if err != nil {
		return FXRate{}, err
	}
	if err := tx.Commit(); err != nil {
		return FXRate{}, err
	}
	return FXRate{Date: date, Currency: currency, Rate: rate, Rederived: n}, nil
}

// Rates lists what is on file, newest first, for one currency or all of them.
func (l *Ledger) Rates(ctx context.Context, currency string) ([]FXRate, error) {
	q := `SELECT date,currency,rate FROM fx_rates`
	var args []any
	if currency != "" {
		q += ` WHERE currency=?`
		args = append(args, strings.ToUpper(strings.TrimSpace(currency)))
	}
	q += ` ORDER BY date DESC, currency`
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FXRate{}
	for rows.Next() {
		var r FXRate
		var raw string
		if err := rows.Scan(&r.Date, &r.Currency, &raw); err != nil {
			return nil, err
		}
		r.Rate = money.Rate(raw)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RemoveRate drops one day's rate and re-derives every transaction in that
// currency that follows the table. The last rate for a currency cannot go
// while such transactions exist, or while a plan is filed in it: they would
// have no reference amount and count as nothing.
func (l *Ledger) RemoveRate(ctx context.Context, currency, date string) error {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM fx_rates WHERE currency=? AND date=?`, currency, date)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no %s rate on %s: %w", currency, date, db.ErrNotFound)
	}
	var left int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM fx_rates WHERE currency=?`, currency).Scan(&left); err != nil {
		return err
	}
	if left == 0 {
		var following int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions WHERE currency=? AND fx_rate=''`,
			currency).Scan(&following); err != nil {
			return err
		}
		if following > 0 {
			return fmt.Errorf("%d %s transactions follow the rate table and this is the last %s rate: file another first", following, currency, currency)
		}
		var planned int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM budgets WHERE currency=? AND planned<>0`,
			currency).Scan(&planned); err != nil {
			return err
		}
		if planned > 0 {
			return fmt.Errorf("%d budget lines are planned in %s and this is the last %s rate: file another first", planned, currency, currency)
		}
		return tx.Commit()
	}
	if _, err := l.rederive(ctx, tx, currency); err != nil {
		return err
	}
	return tx.Commit()
}

// rederive rewrites base_amount, on the transactions and their split lines,
// for every row in currency that follows the table, each at the table's rate
// for its own date. The whole currency is redone rather than a window, since
// a date with no rate before it reads the earliest rate on file, so adding
// or removing the earliest moves rows dated before it. Each amount is
// converted in Go with the exact rate, and an overflow fails the whole
// transaction.
func (l *Ledger) rederive(ctx context.Context, tx *sql.Tx, currency string) (int, error) {
	type row struct {
		id     string
		date   string
		amount int64
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, date, amount FROM transactions WHERE currency=? AND fx_rate=''`, currency)
	if err != nil {
		return 0, err
	}
	var affected []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.date, &r.amount); err != nil {
			rows.Close()
			return 0, err
		}
		affected = append(affected, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, t := range affected {
		rate, ok, err := l.rateOn(ctx, tx, currency, t.date)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, fmt.Errorf("transaction %s: no %s rate on file", t.id, currency)
		}
		ref, err := money.Convert(money.New(t.amount, currency), rate, l.reference)
		if err != nil {
			return 0, fmt.Errorf("transaction %s at %s: %w", t.id, rate, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE transactions SET base_amount=? WHERE id=?`, ref.Minor, t.id); err != nil {
			return 0, err
		}
		lines, err := tx.QueryContext(ctx, `SELECT id, amount FROM splits WHERE transaction_id=?`, t.id)
		if err != nil {
			return 0, err
		}
		var splits []row
		for lines.Next() {
			var s row
			if err := lines.Scan(&s.id, &s.amount); err != nil {
				lines.Close()
				return 0, err
			}
			splits = append(splits, s)
		}
		lines.Close()
		if err := lines.Err(); err != nil {
			return 0, err
		}
		for _, s := range splits {
			ref, err := money.Convert(money.New(s.amount, currency), rate, l.reference)
			if err != nil {
				return 0, fmt.Errorf("split %s at %s: %w", s.id, rate, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE splits SET base_amount=? WHERE id=?`, ref.Minor, s.id); err != nil {
				return 0, err
			}
		}
	}
	return len(affected), nil
}

// RateOn is the rate to read a currency at on a date: the latest one filed on
// or before it. The reference is always 1. The second result is false
// when nothing is on file, which is the difference between a figure that can
// be converted and one that has to be left out.
func (l *Ledger) RateOn(ctx context.Context, currency, date string) (money.Rate, bool, error) {
	return l.rateOn(ctx, l.db, currency, date)
}

// querier is what rateOn needs: the ledger, or a transaction on it.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// rateOn is RateOn against q, so a lookup can run inside a transaction on
// the one connection.
func (l *Ledger) rateOn(ctx context.Context, q querier, currency, date string) (money.Rate, bool, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == l.reference {
		return money.Rate("1"), true, nil
	}
	if date == "" {
		date = l.now().Format(dateFmt)
	}
	var raw string
	err := q.QueryRowContext(ctx,
		`SELECT rate FROM fx_rates WHERE currency=? AND date<=? ORDER BY date DESC LIMIT 1`,
		currency, date).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		// A rate filed after the date is still better than nothing: a balance
		// read today should not vanish because the only rate on file is newer.
		err = q.QueryRowContext(ctx,
			`SELECT rate FROM fx_rates WHERE currency=? ORDER BY date LIMIT 1`, currency).Scan(&raw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return money.Rate(raw), true, nil
}

// RateTable reads every currency's rate as of a date in one pass, for the
// balance sums that would otherwise ask once per account. It covers every
// currency an account or a transaction carries: a transaction can be in a
// currency no account uses, and without its rate it would count as nothing.
func (l *Ledger) RateTable(ctx context.Context, date string) (map[string]money.Rate, error) {
	currencies, err := l.Currencies(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]money.Rate{l.reference: money.Rate("1")}
	for _, currency := range currencies {
		if _, seen := out[currency]; seen {
			continue
		}
		rate, ok, err := l.RateOn(ctx, currency, date)
		if err != nil {
			return nil, err
		}
		if ok {
			out[currency] = rate
		}
	}
	return out, nil
}

// Currencies lists every currency an account or a transaction carries. The
// cursor is drained before returning, since the ledger has one connection.
func (l *Ledger) Currencies(ctx context.Context) ([]string, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT DISTINCT currency FROM transactions UNION SELECT currency FROM accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var currency string
		if err := rows.Scan(&currency); err != nil {
			return nil, err
		}
		out = append(out, currency)
	}
	return out, rows.Err()
}

// ToReference converts an amount to the reference currency with a rate
// already read. An amount already in the reference is returned as it is,
// without consulting the table. The second result is false when that currency
// has no rate on file, which is the difference between a figure that can be
// counted and one that must be left out and said so.
func ToReference(minor int64, currency, reference string, rates map[string]money.Rate) (int64, bool) {
	if currency == reference {
		return minor, true
	}
	rate, ok := rates[currency]
	if !ok {
		return 0, false
	}
	out, err := money.Convert(money.New(minor, currency), rate, reference)
	if err != nil {
		return 0, false
	}
	return out.Minor, true
}

// A first run has no rates at all, so an account in another currency is left
// out of every figure until someone types one in. These are a starting point,
// not a feed: nothing here reaches the network, the numbers were taken on
// referenceDate, and the table shows that date so their age is plain. Correct
// them with: omabudget rate set <currency> <rate>
const referenceDate = "2026-01-01"

// referenceRates is what one unit of each was worth in euro on referenceDate.
var referenceRates = map[string]string{
	"EUR": "1",
	"USD": "0.92",
	"PLN": "0.235",
}

// SeedRates files a starting rate for each quoted currency other than the
// reference, and only while the table is empty. A rate that has been entered is
// never fought, and one that has been removed never comes back. It reports
// how many it filed.
func (l *Ledger) SeedRates(ctx context.Context) (int, error) {
	var have int
	if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fx_rates`).Scan(&have); err != nil {
		return 0, err
	}
	if have > 0 {
		return 0, nil
	}
	inEuro, ok := new(big.Rat).SetString(referenceRates[l.reference])
	if !ok {
		// A reference outside the quoted set has nothing to convert through,
		// which is honest: no rate beats a guessed one.
		return 0, nil
	}
	filed := 0
	currencies := make([]string, 0, len(referenceRates))
	for c := range referenceRates {
		currencies = append(currencies, c)
	}
	slices.Sort(currencies)
	for _, currency := range currencies {
		if currency == l.reference {
			continue
		}
		ref, ok := new(big.Rat).SetString(referenceRates[currency])
		if !ok {
			continue
		}
		rate := new(big.Rat).Quo(ref, inEuro)
		if _, err := l.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO fx_rates (date,currency,rate) VALUES (?,?,?)`,
			referenceDate, currency, trimRate(rate.FloatString(6))); err != nil {
			return filed, err
		}
		filed++
	}
	return filed, nil
}

// trimRate drops the zeros a fixed number of decimal places leaves behind, so
// the table reads 0.92 rather than 0.920000.
func trimRate(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
