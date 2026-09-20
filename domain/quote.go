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
	"github.com/karamble/omarchy-omabudget/feed"
	"github.com/karamble/omarchy-omabudget/money"
)

// Applying a fetched quote to the table. Nothing here reaches the network:
// the quote arrives as a value, and only the currencies in use are ever
// written, so a fetch of the whole set discloses nothing about what is held.

// How far a quote is trusted. A publication older than staleDays is held
// whole: the longest gap the bank leaves is four or five days, so anything
// past ten is a broken source or a wrong clock. A baseline older than
// baselineDays is not compared against: currencies move a long way over
// years, and a person who has not refreshed in three years files a tenfold
// move without a fight.
const (
	staleDays    = 10
	baselineDays = 90
)

// The band, as ratios of the new rate to the baseline. Not a percentage
// cap: currencies really do move that far. The likeliest bug is the
// inversion error, which squares the ratio of every currency at once (JPY
// 32700x, PLN 19x, USD 1.31x), so any two currencies in use trip the batch
// hold. No quoted currency has moved fourfold in ninety days, so the lone
// hold never fires on a real market, only on a redenomination, which is
// exactly when a person should look.
var (
	movedUp   = big.NewRat(5, 4)
	movedDown = big.NewRat(4, 5)
	aloneUp   = big.NewRat(4, 1)
	aloneDown = big.NewRat(1, 4)
)

// Applied is what one press did with a quote, currency by currency. Filed
// were written; Unchanged were already on file as quoted, so a second press
// on the same day is honestly a no-op; Kept carry a typed rate for that day;
// Held were refused pending a look; Unquoted are in use but not carried by
// the source. Notes name filed rates that moved far, and Rederived counts
// the transactions the filing moved.
type Applied struct {
	Filed     []string `json:"filed"`
	Unchanged []string `json:"unchanged"`
	Kept      []string `json:"kept"`
	Held      []Held   `json:"held"`
	Unquoted  []string `json:"unquoted"`
	Notes     []string `json:"notes"`
	Rederived int      `json:"rederived"`
}

// Held is a quoted rate that was not filed: the rate and its date, the rate
// the ledger would have used for that date when it has one, and why.
type Held struct {
	Currency     string     `json:"currency"`
	Rate         money.Rate `json:"rate"`
	Date         string     `json:"date"`
	Previous     money.Rate `json:"previous,omitempty"`
	PreviousDate string     `json:"previousDate,omitempty"`
	Reason       string     `json:"reason"`
}

// candidate is a quoted currency in use that passed the checks needing no
// comparison, with its baseline when the table has one.
type candidate struct {
	currency string
	rate     money.Rate
	baseline rateRow
	hasBase  bool
	// compared is set when the baseline is recent enough to judge by, and
	// moved and alone are the band applied to the ratio.
	compared     bool
	moved, alone bool
}

func (c candidate) held(date, reason string) Held {
	h := Held{Currency: c.currency, Rate: c.rate, Date: date, Reason: reason}
	if c.hasBase {
		h.Previous, h.PreviousDate = c.baseline.rate, c.baseline.date
	}
	return h
}

// ApplyQuote files a quote from source for the currencies in use, under
// the date the source published it, and reports what it did with each. In
// order: a currency the quote does not carry is unquoted; a rate typed by
// hand for that day is kept; a publication more than staleDays old holds
// everything else; a rate already on file as quoted is unchanged. What is
// left is compared with the rate the ledger would have used for that date,
// when that baseline is within baselineDays. With two or more compared and
// more than half of them moved past a quarter, the batch is held: a bad
// response corrupts all of it. Otherwise a rate moved fourfold is held and
// named, one moved past a quarter is filed and noted, and the rest are
// filed silently. Known cost: a genuine collapse of the reference moves
// every rate together and holds the batch, which is what AcceptRate is for.
func (l *Ledger) ApplyQuote(ctx context.Context, q feed.Quote, source string, use []string) (Applied, error) {
	out := Applied{Filed: []string{}, Unchanged: []string{}, Kept: []string{}, Held: []Held{}, Unquoted: []string{}, Notes: []string{}}
	if err := checkFeed(source); err != nil {
		return Applied{}, err
	}
	published, err := time.Parse(dateFmt, q.Date)
	if err != nil {
		return Applied{}, fmt.Errorf("quote date %q must be YYYY-MM-DD", q.Date)
	}
	if _, ok := q.Rates[l.reference]; !ok && q.Base != l.reference {
		return Applied{}, fmt.Errorf("the source does not quote %s, so nothing can be filed against it", l.reference)
	}
	quoted, err := q.Against(l.reference)
	if err != nil {
		return Applied{}, err
	}
	// Age is measured in whole days against the ledger's clock, so a test
	// can move it.
	now := l.now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	age := int(today.Sub(published).Hours() / 24)
	stale := age > staleDays

	wanted := []string{}
	seen := map[string]bool{}
	for _, c := range use {
		c = strings.ToUpper(strings.TrimSpace(c))
		if len(c) != 3 || c == l.reference || seen[c] {
			continue
		}
		seen[c] = true
		wanted = append(wanted, c)
	}
	slices.Sort(wanted)

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Applied{}, err
	}
	defer tx.Rollback()
	var cands []candidate
	for _, currency := range wanted {
		rate, ok := quoted[currency]
		if !ok {
			out.Unquoted = append(out.Unquoted, currency)
			continue
		}
		rows, err := l.ratesOf(ctx, tx, currency)
		if err != nil {
			return Applied{}, err
		}
		c := candidate{currency: currency, rate: rate}
		c.baseline, c.hasBase = pick(rows, q.Date)
		var onDate *rateRow
		for i := range rows {
			if rows[i].date == q.Date {
				onDate = &rows[i]
			}
		}
		if onDate != nil && onDate.source == SourceManual {
			out.Kept = append(out.Kept, currency)
			continue
		}
		if stale {
			out.Held = append(out.Held, c.held(q.Date, fmt.Sprintf("published %s, %d days ago: too old to file", q.Date, age)))
			continue
		}
		if onDate != nil && onDate.source == source && onDate.rate.Equal(rate) {
			out.Unchanged = append(out.Unchanged, currency)
			continue
		}
		if c.hasBase && daysApart(c.baseline.date, q.Date) <= baselineDays {
			from, ok1 := new(big.Rat).SetString(string(c.baseline.rate))
			to, ok2 := new(big.Rat).SetString(string(rate))
			if ok1 && ok2 && from.Sign() > 0 {
				r := new(big.Rat).Quo(to, from)
				c.compared = true
				c.moved = r.Cmp(movedUp) > 0 || r.Cmp(movedDown) < 0
				c.alone = r.Cmp(aloneUp) >= 0 || r.Cmp(aloneDown) <= 0
			}
		}
		cands = append(cands, c)
	}

	compared, moved := 0, 0
	for _, c := range cands {
		if c.compared {
			compared++
			if c.moved {
				moved++
			}
		}
	}
	if compared >= 2 && moved*2 > compared {
		reason := fmt.Sprintf("%d of %d rates moved more than a quarter at once, which looks like a bad response rather than a market", moved, compared)
		for _, c := range cands {
			out.Held = append(out.Held, c.held(q.Date, reason))
		}
		return out, nil
	}
	for _, c := range cands {
		if c.alone {
			out.Held = append(out.Held, c.held(q.Date, fmt.Sprintf("moved from %s on %s to %s, more than fourfold", c.baseline.rate, c.baseline.date, c.rate)))
			continue
		}
		if c.moved {
			out.Notes = append(out.Notes, fmt.Sprintf("%s moved from %s on %s to %s", c.currency, c.baseline.rate, c.baseline.date, c.rate))
		}
		if err := l.file(ctx, tx, c.currency, c.rate, q.Date, source); err != nil {
			return Applied{}, err
		}
		n, err := l.rederive(ctx, tx, c.currency)
		if err != nil {
			return Applied{}, err
		}
		out.Rederived += n
		out.Filed = append(out.Filed, c.currency)
	}
	if err := tx.Commit(); err != nil {
		return Applied{}, err
	}
	return out, nil
}

// AcceptRate files one held rate by hand, stamped with the source that
// quoted it, and re-derives what follows the table. A rate typed for that
// day is not overwritten: that is the one promise a fetch makes.
func (l *Ledger) AcceptRate(ctx context.Context, currency string, rate money.Rate, date, source string) (FXRate, error) {
	if err := checkFeed(source); err != nil {
		return FXRate{}, err
	}
	currency, date, err := l.checkRate(currency, rate, date)
	if err != nil {
		return FXRate{}, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return FXRate{}, err
	}
	defer tx.Rollback()
	var have string
	err = tx.QueryRowContext(ctx, `SELECT source FROM fx_rates WHERE currency=? AND date=?`, currency, date).Scan(&have)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return FXRate{}, err
	}
	if have == SourceManual {
		return FXRate{}, fmt.Errorf("the %s rate on %s was typed by hand: remove it first", currency, date)
	}
	if err := l.file(ctx, tx, currency, rate, date, source); err != nil {
		return FXRate{}, err
	}
	n, err := l.rederive(ctx, tx, currency)
	if err != nil {
		return FXRate{}, err
	}
	if err := tx.Commit(); err != nil {
		return FXRate{}, err
	}
	return FXRate{Date: date, Currency: currency, Rate: rate, Source: source, Rederived: n}, nil
}

// checkFeed refuses a source that is not a feed, so a fetched rate is never
// stamped as typed or shipped.
func checkFeed(source string) error {
	if source == "" || source == SourceManual || source == SourceSeed {
		return fmt.Errorf("source %q is not a feed", source)
	}
	return nil
}

// daysApart is the distance between two dates in whole days.
func daysApart(a, b string) int {
	ta, err := time.Parse(dateFmt, a)
	if err != nil {
		return baselineDays + 1
	}
	tb, err := time.Parse(dateFmt, b)
	if err != nil {
		return baselineDays + 1
	}
	d := int(tb.Sub(ta).Hours() / 24)
	if d < 0 {
		d = -d
	}
	return d
}

// CurrenciesInUse lists every currency other than the reference that an
// account or a transaction carries or a plan is filed in, sorted: what a
// fetched quote is applied to.
func (l *Ledger) CurrenciesInUse(ctx context.Context) ([]string, error) {
	carried, err := l.Currencies(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT DISTINCT currency FROM budgets WHERE planned<>0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	out := []string{}
	add := func(currency string) {
		if currency == l.reference || seen[currency] {
			return
		}
		seen[currency] = true
		out = append(out, currency)
	}
	for _, currency := range carried {
		add(currency)
	}
	for rows.Next() {
		var currency string
		if err := rows.Scan(&currency); err != nil {
			return nil, err
		}
		add(currency)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Sort(out)
	return out, nil
}

// Meta keys for the one record kept of a fetch: when it last ran, from
// which source, and which host answered.
const (
	metaFetchAt     = "rate_fetch_at"
	metaFetchSource = "rate_fetch_source"
	metaFetchHost   = "rate_fetch_host"
)

// Fetch is the record of the last fetch, which is all the ledger keeps about
// the network: when, from which source, and which host answered.
type Fetch struct {
	At     string `json:"at"`
	Source string `json:"source"`
	Host   string `json:"host"`
}

// StampFetch records that a fetch ran now, whatever came of it, since the
// connection was made either way.
func (l *Ledger) StampFetch(ctx context.Context, source, host string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, kv := range [][2]string{{metaFetchAt, l.stamp()}, {metaFetchSource, source}, {metaFetchHost, host}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LastFetch reads that record. The second result is false when no fetch has
// ever run, which is what lets the app say the network has never been used.
func (l *Ledger) LastFetch(ctx context.Context) (Fetch, bool, error) {
	var f Fetch
	at, err := l.db.Meta(ctx, metaFetchAt)
	if errors.Is(err, db.ErrNotFound) {
		return Fetch{}, false, nil
	}
	if err != nil {
		return Fetch{}, false, err
	}
	f.At = at
	if f.Source, err = l.db.Meta(ctx, metaFetchSource); err != nil && !errors.Is(err, db.ErrNotFound) {
		return Fetch{}, false, err
	}
	if f.Host, err = l.db.Meta(ctx, metaFetchHost); err != nil && !errors.Is(err, db.ErrNotFound) {
		return Fetch{}, false, err
	}
	return f, true, nil
}
