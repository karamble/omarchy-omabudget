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

// The rate table, spec 8. A transaction freezes its own rate at entry, so
// nothing here ever moves a base amount that is already written: these rates
// are what a new entry falls back on, and what a balance in another currency
// is read at.

// FXRate is one day's rate for a currency against the base.
type FXRate struct {
	Date     string     `json:"date"`
	Currency string     `json:"currency"`
	Rate     money.Rate `json:"rate"`
}

// SetRate files a rate for a currency on a date, replacing that day's if it
// has one. The date defaults to today.
func (l *Ledger) SetRate(ctx context.Context, currency string, rate money.Rate, date string) (FXRate, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return FXRate{}, fmt.Errorf("currency %q must be a three-letter code", currency)
	}
	if currency == l.base {
		return FXRate{}, fmt.Errorf("%s is the base currency: its rate is always 1", currency)
	}
	if _, ok := new(big.Rat).SetString(string(rate)); !ok {
		return FXRate{}, fmt.Errorf("rate %q is not a number", rate)
	}
	// Convert proves the rate is usable and positive, which is the only shape
	// a base amount can be frozen from.
	if _, err := money.Convert(money.New(100, currency), rate, l.base); err != nil {
		return FXRate{}, err
	}
	if date == "" {
		date = l.now().Format(dateFmt)
	}
	if _, err := time.Parse(dateFmt, date); err != nil {
		return FXRate{}, fmt.Errorf("date %q must be YYYY-MM-DD", date)
	}
	_, err := l.db.ExecContext(ctx, `INSERT INTO fx_rates (date,currency,rate) VALUES (?,?,?)
		ON CONFLICT(date,currency) DO UPDATE SET rate=excluded.rate`, date, currency, string(rate))
	if err != nil {
		return FXRate{}, err
	}
	return FXRate{Date: date, Currency: currency, Rate: rate}, nil
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

// RemoveRate drops one day's rate.
func (l *Ledger) RemoveRate(ctx context.Context, currency, date string) error {
	res, err := l.db.ExecContext(ctx, `DELETE FROM fx_rates WHERE currency=? AND date=?`,
		strings.ToUpper(strings.TrimSpace(currency)), date)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no %s rate on %s: %w", currency, date, db.ErrNotFound)
	}
	return nil
}

// RateOn is the rate to read a currency at on a date: the latest one filed on
// or before it. The base currency is always 1. The second result is false
// when nothing is on file, which is the difference between a figure that can
// be converted and one that has to be left out.
func (l *Ledger) RateOn(ctx context.Context, currency, date string) (money.Rate, bool, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == l.base {
		return money.Rate("1"), true, nil
	}
	if date == "" {
		date = l.now().Format(dateFmt)
	}
	var raw string
	err := l.db.QueryRowContext(ctx,
		`SELECT rate FROM fx_rates WHERE currency=? AND date<=? ORDER BY date DESC LIMIT 1`,
		currency, date).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		// A rate filed after the date is still better than nothing: a balance
		// read today should not vanish because the only rate on file is newer.
		err = l.db.QueryRowContext(ctx,
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
// balance sums that would otherwise ask once per account.
func (l *Ledger) RateTable(ctx context.Context, date string) (map[string]money.Rate, error) {
	accounts, err := l.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]money.Rate{l.base: money.Rate("1")}
	for _, a := range accounts {
		if _, seen := out[a.Currency]; seen {
			continue
		}
		rate, ok, err := l.RateOn(ctx, a.Currency, date)
		if err != nil {
			return nil, err
		}
		if ok {
			out[a.Currency] = rate
		}
	}
	return out, nil
}

// InBase converts an amount to the base currency with a rate already read.
// The second result is false when that currency has no rate on file, which is
// the difference between a figure that can be counted and one that must be
// left out and said so.
func InBase(minor int64, currency, base string, rates map[string]money.Rate) (int64, bool) {
	rate, ok := rates[currency]
	if !ok {
		return 0, false
	}
	out, err := money.Convert(money.New(minor, currency), rate, base)
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

// SeedRates files a starting rate for each reference currency other than the
// base, and only while the table is empty. A rate that has been entered is
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
	inEuro, ok := new(big.Rat).SetString(referenceRates[l.base])
	if !ok {
		// A base outside the reference set has nothing to convert through,
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
		if currency == l.base {
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
