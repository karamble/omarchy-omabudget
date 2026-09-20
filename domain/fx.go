package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/money"
)

// The rate table, spec 8. Every rate is quoted against one reference
// currency, recorded in the ledger when it was first opened and never moved,
// so a rate means the same thing whatever currency figures are shown in. A
// transaction either follows this table or carries a rate of its own; the
// table is what a balance in another currency is read at. Rates get here by
// being typed, shipped with the binary, or fetched from the source chosen
// in settings when a person asks; nothing in this package reaches the
// network, and nothing fetches on its own.

// FXRate is one day's rate for a currency against the reference. Source is
// where it came from, and Rederived counts the transactions a change to it
// moved.
type FXRate struct {
	Date      string     `json:"date"`
	Currency  string     `json:"currency"`
	Rate      money.Rate `json:"rate"`
	Source    string     `json:"source"`
	Rederived int        `json:"rederived,omitempty"`
}

// Where a rate can come from. A feed names itself.
const (
	SourceManual = "manual"
	SourceSeed   = "seed"
)

// SetRate files a rate for a currency on a date, replacing that day's if it
// has one. The date defaults to today. Every transaction in that currency
// that follows the table is re-derived in the same transaction; a row that
// carries a rate entered by hand is left alone.
func (l *Ledger) SetRate(ctx context.Context, currency string, rate money.Rate, date string) (FXRate, error) {
	currency, date, err := l.checkRate(currency, rate, date)
	if err != nil {
		return FXRate{}, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return FXRate{}, err
	}
	defer tx.Rollback()
	if err := l.file(ctx, tx, currency, rate, date, SourceManual); err != nil {
		return FXRate{}, err
	}
	n, err := l.rederive(ctx, tx, currency)
	if err != nil {
		return FXRate{}, err
	}
	if err := tx.Commit(); err != nil {
		return FXRate{}, err
	}
	return FXRate{Date: date, Currency: currency, Rate: rate, Source: SourceManual, Rederived: n}, nil
}

// checkRate is what any rate must pass before it is filed: a three-letter
// currency other than the reference, a positive number, and a date, which
// defaults to today. It reports the currency and date as they will be kept.
func (l *Ledger) checkRate(currency string, rate money.Rate, date string) (string, string, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return "", "", fmt.Errorf("currency %q must be a three-letter code", currency)
	}
	if currency == l.reference {
		return "", "", fmt.Errorf("%s is the rate reference: its rate is always 1", currency)
	}
	if _, ok := new(big.Rat).SetString(string(rate)); !ok {
		return "", "", fmt.Errorf("rate %q is not a number", rate)
	}
	// Convert proves the rate is usable and positive, which is the only shape
	// a reference amount can be derived from.
	if _, err := money.Convert(money.New(100, currency), rate, l.reference); err != nil {
		return "", "", err
	}
	if date == "" {
		date = l.now().Format(dateFmt)
	}
	if _, err := time.Parse(dateFmt, date); err != nil {
		return "", "", fmt.Errorf("date %q must be YYYY-MM-DD", date)
	}
	return currency, date, nil
}

// Rates lists what is on file, newest first, for one currency or all of them.
func (l *Ledger) Rates(ctx context.Context, currency string) ([]FXRate, error) {
	q := `SELECT date,currency,rate,source FROM fx_rates`
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
		if err := rows.Scan(&r.Date, &r.Currency, &raw, &r.Source); err != nil {
			return nil, err
		}
		r.Rate = money.Rate(raw)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestRates is the newest rate on file for each currency, by currency,
// which is all a form needs to preview an entry.
func (l *Ledger) LatestRates(ctx context.Context) ([]FXRate, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT f.date, f.currency, f.rate, f.source FROM fx_rates f
		JOIN (SELECT currency, MAX(date) AS date FROM fx_rates GROUP BY currency) n
		ON f.currency=n.currency AND f.date=n.date ORDER BY f.currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FXRate{}
	for rows.Next() {
		var r FXRate
		var raw string
		if err := rows.Scan(&r.Date, &r.Currency, &raw, &r.Source); err != nil {
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
// or removing the earliest moves rows dated before it. The table is read
// once and each row resolved in Go by the rule rateOn applies; each amount
// is converted with the exact rate, and an overflow fails the whole
// transaction. Every cursor is drained before the next query, since the
// ledger has one connection.
func (l *Ledger) rederive(ctx context.Context, tx *sql.Tx, currency string) (int, error) {
	rates, err := l.ratesOf(ctx, tx, currency)
	if err != nil {
		return 0, err
	}
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
	lines, err := tx.QueryContext(ctx, `SELECT s.id, s.transaction_id, s.amount FROM splits s
		JOIN transactions t ON t.id=s.transaction_id WHERE t.currency=? AND t.fx_rate=''`, currency)
	if err != nil {
		return 0, err
	}
	splits := map[string][]row{}
	for lines.Next() {
		var s row
		var parent string
		if err := lines.Scan(&s.id, &parent, &s.amount); err != nil {
			lines.Close()
			return 0, err
		}
		splits[parent] = append(splits[parent], s)
	}
	lines.Close()
	if err := lines.Err(); err != nil {
		return 0, err
	}
	for _, t := range affected {
		on, ok := pick(rates, t.date)
		if !ok {
			return 0, fmt.Errorf("transaction %s: no %s rate on file", t.id, currency)
		}
		ref, err := money.Convert(money.New(t.amount, currency), on.rate, l.reference)
		if err != nil {
			return 0, fmt.Errorf("transaction %s at %s: %w", t.id, on.rate, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE transactions SET base_amount=? WHERE id=?`, ref.Minor, t.id); err != nil {
			return 0, err
		}
		for _, s := range splits[t.id] {
			ref, err := money.Convert(money.New(s.amount, currency), on.rate, l.reference)
			if err != nil {
				return 0, fmt.Errorf("split %s at %s: %w", s.id, on.rate, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE splits SET base_amount=? WHERE id=?`, ref.Minor, s.id); err != nil {
				return 0, err
			}
		}
	}
	return len(affected), nil
}

// rateRow is one row of the table as read for resolving in Go.
type rateRow struct {
	date   string
	rate   money.Rate
	source string
}

// ratesOf reads every rate on file for a currency, oldest first, drained
// before returning.
func (l *Ledger) ratesOf(ctx context.Context, q querier, currency string) ([]rateRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT date, rate, source FROM fx_rates WHERE currency=? ORDER BY date`, currency)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rateRow
	for rows.Next() {
		var r rateRow
		var raw string
		if err := rows.Scan(&r.date, &raw, &r.source); err != nil {
			return nil, err
		}
		r.rate = money.Rate(raw)
		out = append(out, r)
	}
	return out, rows.Err()
}

// pick is the rule rateOn applies, over rows already read oldest first: the
// latest on or before the date, else the earliest, else nothing.
func pick(rates []rateRow, date string) (rateRow, bool) {
	if len(rates) == 0 {
		return rateRow{}, false
	}
	i := sort.Search(len(rates), func(i int) bool { return rates[i].date > date })
	if i == 0 {
		return rates[0], true
	}
	return rates[i-1], true
}

// RateOn is the rate to read a currency at on a date: the latest one filed on
// or before it. The reference is always 1. The second result is false
// when nothing is on file, which is the difference between a figure that can
// be converted and one that has to be left out.
func (l *Ledger) RateOn(ctx context.Context, currency, date string) (money.Rate, bool, error) {
	return l.rateOn(ctx, l.db, currency, date)
}

// querier is what a lookup needs: the ledger, or a transaction on it.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
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

// SeedFor files the shipped starting rate for a currency the first time it
// comes into use, dated the day the quote was published, and reports whether
// it did. Seeding is closed for a currency once it has a marker or any rate
// on file, whoever filed it, and the marker outlives the row. So a rate that
// was typed and then removed does not come back as the shipped one: a
// deleted rate never returns, whatever its source. A currency the quote does
// not carry, or a reference it does not quote, gets nothing and no marker.
func (l *Ledger) SeedFor(ctx context.Context, currency string) (bool, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if l.seed == nil || len(currency) != 3 || currency == l.reference {
		return false, nil
	}
	if _, err := l.db.Meta(ctx, db.MetaRateSeeded(currency)); err == nil {
		return false, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return false, err
	}
	var have int
	if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fx_rates WHERE currency=?`, currency).Scan(&have); err != nil {
		return false, err
	}
	if have > 0 {
		return false, nil
	}
	if _, quoted := l.seed.Rates[l.reference]; !quoted && l.reference != l.seed.Base {
		return false, nil
	}
	if _, quoted := l.seed.Rates[currency]; !quoted && currency != l.seed.Base {
		return false, nil
	}
	rates, err := l.seed.Against(l.reference)
	if err != nil {
		return false, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Nothing follows the table in a currency with no rate, so there is
	// nothing to re-derive.
	if err := l.file(ctx, tx, currency, rates[currency], l.seed.Date, SourceSeed); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// file writes one rate, replacing that day's if it has one, and closes
// seeding for the currency, so the shipped rate stays out of a currency that
// has had a rate of any source.
func (l *Ledger) file(ctx context.Context, tx *sql.Tx, currency string, rate money.Rate, date, source string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO fx_rates (date,currency,rate,source) VALUES (?,?,?,?)
		ON CONFLICT(date,currency) DO UPDATE SET rate=excluded.rate, source=excluded.source`,
		date, currency, string(rate), source); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO meta (key, value) VALUES (?, ?)`,
		db.MetaRateSeeded(currency), date)
	return err
}
