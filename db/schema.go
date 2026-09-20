package db

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"strings"

	"github.com/karamble/omarchy-omabudget/money"
)

// migration is one numbered, forward-only step. pre runs first, outside any
// transaction, for what SQLite refuses inside one: a VACUUM INTO snapshot, or
// a pragma such as foreign_keys, which is silently ignored mid-transaction.
// statements then run in order inside a transaction; after runs in the same
// transaction for anything that needs Go rather than SQL, such as seeding.
type migration struct {
	version    int
	name       string
	pre        func(ctx context.Context, d *DB) error
	statements []string
	after      func(ctx context.Context, tx *sql.Tx, opts Options) error
}

// Amounts are integer minor units (see the money package). Dates are ISO
// strings, YYYY-MM-DD, so they sort and compare as text. Money and dates are
// never stored as REAL.
var migrations = []migration{
	{
		version: 1,
		name:    "ledger",
		statements: []string{
			// ---- accounts, spec 1.1
			`CREATE TABLE accounts (
				id                   TEXT PRIMARY KEY,
				name                 TEXT NOT NULL,
				type                 TEXT NOT NULL CHECK (type IN (
				                       'checking','savings','cash','credit_card','loan',
				                       'investment','prepaid','receivable','payable')),
				currency             TEXT NOT NULL,
				opening_balance      INTEGER NOT NULL DEFAULT 0,
				opening_date         TEXT NOT NULL,
				is_active            INTEGER NOT NULL DEFAULT 1,
				include_in_net_worth INTEGER NOT NULL DEFAULT 1,
				institution          TEXT NOT NULL DEFAULT '',
				identifier_last4     TEXT NOT NULL DEFAULT '',
				sort_order           INTEGER NOT NULL DEFAULT 0,
				colour               TEXT NOT NULL DEFAULT '',
				icon                 TEXT NOT NULL DEFAULT '',
				low_balance          INTEGER,
				created_at           TEXT NOT NULL,
				modified_at          TEXT NOT NULL,
				deleted_at           TEXT
			)`,

			// ---- categories, spec 1.4: a two-level tree
			`CREATE TABLE categories (
				id                       TEXT PRIMARY KEY,
				name                     TEXT NOT NULL,
				parent_id                TEXT REFERENCES categories(id),
				kind                     TEXT NOT NULL CHECK (kind IN ('income','expense')),
				icon                     TEXT NOT NULL DEFAULT '',
				colour                   TEXT NOT NULL DEFAULT '',
				is_archived              INTEGER NOT NULL DEFAULT 0,
				is_system                INTEGER NOT NULL DEFAULT 0,
				default_budget_behaviour TEXT NOT NULL DEFAULT 'monthly' CHECK (
				                           default_budget_behaviour IN ('monthly','rollover','goal','untracked')),
				exclude_from_statistics  INTEGER NOT NULL DEFAULT 0,
				sort_order               INTEGER NOT NULL DEFAULT 0,
				-- A goal category saves toward goal_target by the month
				-- goal_due, spec 5.3.
				goal_target              INTEGER NOT NULL DEFAULT 0,
				goal_due                 TEXT NOT NULL DEFAULT '',
				deleted_at               TEXT
			)`,
			`CREATE INDEX categories_parent ON categories(parent_id)`,

			// ---- payees, spec 1.5
			`CREATE TABLE payees (
				id                  TEXT PRIMARY KEY,
				name                TEXT NOT NULL,
				default_category_id TEXT REFERENCES categories(id),
				notes               TEXT NOT NULL DEFAULT '',
				deleted_at          TEXT
			)`,
			`CREATE TABLE payee_aliases (
				payee_id TEXT NOT NULL REFERENCES payees(id) ON DELETE CASCADE,
				alias    TEXT NOT NULL,
				PRIMARY KEY (payee_id, alias)
			)`,

			// ---- tags, spec 1.6
			`CREATE TABLE tags (
				id         TEXT PRIMARY KEY,
				name       TEXT NOT NULL UNIQUE,
				deleted_at TEXT
			)`,

			// ---- transactions, spec 1.2 and 8
			`CREATE TABLE transactions (
				id                    TEXT PRIMARY KEY,
				kind                  TEXT NOT NULL CHECK (kind IN ('expense','income','transfer')),
				date                  TEXT NOT NULL,
				booking_date          TEXT,
				amount                INTEGER NOT NULL,
				currency              TEXT NOT NULL,
				fx_rate               TEXT NOT NULL DEFAULT '1',
				base_amount           INTEGER NOT NULL,
				account_id            TEXT NOT NULL REFERENCES accounts(id),
				counter_account_id    TEXT REFERENCES accounts(id),
				counter_amount        INTEGER,
				category_id           TEXT REFERENCES categories(id),
				payee_id              TEXT REFERENCES payees(id),
				description           TEXT NOT NULL DEFAULT '',
				notes                 TEXT NOT NULL DEFAULT '',
				status                TEXT NOT NULL DEFAULT 'cleared' CHECK (status IN ('pending','cleared','reconciled')),
				recurring_rule_id     TEXT,
				is_recurring_instance INTEGER NOT NULL DEFAULT 0,
				import_hash           TEXT,
				import_batch_id       TEXT,
				created_at            TEXT NOT NULL,
				modified_at           TEXT NOT NULL,
				deleted_at            TEXT,
				CHECK (amount <> 0),
				CHECK (kind <> 'transfer' OR counter_account_id IS NOT NULL),
				CHECK (kind <> 'transfer' OR counter_account_id <> account_id),
				CHECK (kind <> 'transfer' OR category_id IS NULL)
			)`,
			`CREATE INDEX transactions_date ON transactions(date)`,
			`CREATE INDEX transactions_account ON transactions(account_id, date)`,
			`CREATE INDEX transactions_category ON transactions(category_id, date)`,
			`CREATE INDEX transactions_import ON transactions(import_hash)`,

			// ---- splits, spec 1.3: statistics operate on lines
			`CREATE TABLE splits (
				id             TEXT PRIMARY KEY,
				transaction_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
				category_id    TEXT NOT NULL REFERENCES categories(id),
				amount         INTEGER NOT NULL CHECK (amount <> 0),
				base_amount    INTEGER NOT NULL,
				note           TEXT NOT NULL DEFAULT '',
				sort_order     INTEGER NOT NULL DEFAULT 0
			)`,
			`CREATE INDEX splits_transaction ON splits(transaction_id)`,
			`CREATE INDEX splits_category ON splits(category_id)`,

			`CREATE TABLE transaction_tags (
				transaction_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
				tag_id         TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
				PRIMARY KEY (transaction_id, tag_id)
			)`,
			`CREATE TABLE split_tags (
				split_id TEXT NOT NULL REFERENCES splits(id) ON DELETE CASCADE,
				tag_id   TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
				PRIMARY KEY (split_id, tag_id)
			)`,

			// ---- budgets, spec 1.7 and 5
			`CREATE TABLE budgets (
				id               TEXT PRIMARY KEY,
				category_id      TEXT NOT NULL REFERENCES categories(id),
				period           TEXT NOT NULL,
				planned          INTEGER NOT NULL DEFAULT 0,
				rollover_enabled INTEGER NOT NULL DEFAULT 0,
				rollover_in      INTEGER NOT NULL DEFAULT 0,
				note             TEXT NOT NULL DEFAULT '',
				UNIQUE (category_id, period)
			)`,
			`CREATE INDEX budgets_period ON budgets(period)`,

			// ---- recurring rules, spec 1.8. The template is the transaction to
			// generate, stored as JSON so it carries every field without a
			// second copy of the transactions schema.
			`CREATE TABLE recurring_rules (
				id               TEXT PRIMARY KEY,
				name             TEXT NOT NULL,
				template         TEXT NOT NULL,
				frequency        TEXT NOT NULL CHECK (frequency IN (
				                   'daily','weekly','biweekly','monthly','quarterly','semiannual','annual','custom')),
				interval_days    INTEGER,
				day_rule         TEXT NOT NULL DEFAULT '',
				start_date       TEXT NOT NULL,
				end_date         TEXT,
				occurrence_count INTEGER,
				auto_post        INTEGER NOT NULL DEFAULT 0,
				lead_days        INTEGER NOT NULL DEFAULT 3,
				variable_amount  INTEGER NOT NULL DEFAULT 0,
				is_active        INTEGER NOT NULL DEFAULT 1,
				last_posted      TEXT,
				next_due         TEXT,
				deleted_at       TEXT
			)`,
			`CREATE INDEX recurring_next_due ON recurring_rules(next_due)`,

			// ---- exchange rates, spec 8: a per-day table the user maintains
			`CREATE TABLE fx_rates (
				date     TEXT NOT NULL,
				currency TEXT NOT NULL,
				rate     TEXT NOT NULL,
				PRIMARY KEY (date, currency)
			)`,
		},
		// The taxonomy is part of the structure: a ledger with no categories
		// cannot book anything.
		after: seedCategories,
	},
	{
		version: 2,
		name:    "rate reference",
		statements: []string{
			// ---- meta: facts about the ledger that must travel with it
			`CREATE TABLE meta (
				key   TEXT PRIMARY KEY,
				value TEXT NOT NULL
			)`,
		},
		// Every rate on file so far is quoted against the base currency, so
		// that is what the reference is pinned to. It never moves again.
		after: pinRateReference,
	},
	{
		version: 3,
		name:    "rate marker",
		// From here on fx_rate is empty on a row that follows the rate
		// table, and holds the rate only when it was entered by hand.
		after: blankDerivedRates,
	},
	{
		version: 4,
		name:    "budget currency",
		statements: []string{
			// A plan is kept in the currency it was typed in, so a rate
			// correction never rewrites what was planned.
			`ALTER TABLE budgets ADD COLUMN currency TEXT NOT NULL DEFAULT ''`,
		},
		// Every plan filed before this column existed was typed in the
		// reference.
		after: fillBudgetCurrency,
	},
	{
		version: 5,
		name:    "rate source",
		statements: []string{
			// Where a rate came from: typed, shipped with the binary, or
			// fetched from a named feed.
			`ALTER TABLE fx_rates ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'
				CHECK (source IN ('manual','seed','ecb','frankfurter'))`,
			// Every lookup asks for one currency by date; the primary key is
			// ordered by date first, so without this it scans.
			`CREATE INDEX fx_rates_currency_date ON fx_rates(currency, date)`,
		},
		after: labelRateSources,
	},
}

// startingRates is what the seeding before version 5 filed: one unit of each
// in euro on startingDate, crossed into the reference and written to six
// decimals. It is kept here so those rows can be told from typed ones.
const startingDate = "2026-01-01"

var startingRates = map[string]string{
	"EUR": "1",
	"USD": "0.92",
	"PLN": "0.235",
}

// labelRateSources marks the rows the old seeding could have written as
// seed and leaves every other row manual. It then records a marker for
// every currency that seeding has offered, or that is on file, so a rate
// removed from an old ledger is never offered again.
func labelRateSources(ctx context.Context, tx *sql.Tx, _ Options) error {
	var reference string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, MetaRateReference).Scan(&reference); err != nil {
		return fmt.Errorf("reading the rate reference: %w", err)
	}
	var accounts, rates int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&accounts); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM fx_rates`).Scan(&rates); err != nil {
		return err
	}
	offered := map[string]bool{}
	// Seeding ran at every daemon start, so a ledger that has been used
	// under a quoted reference had the whole starting set offered to it.
	if inEuro, ok := new(big.Rat).SetString(startingRates[reference]); ok && accounts+rates > 0 {
		for currency, quote := range startingRates {
			if currency == reference {
				continue
			}
			offered[currency] = true
			ref, _ := new(big.Rat).SetString(quote)
			rate := new(big.Rat).Quo(ref, inEuro).FloatString(6)
			rate = strings.TrimSuffix(strings.TrimRight(rate, "0"), ".")
			if _, err := tx.ExecContext(ctx, `UPDATE fx_rates SET source='seed' WHERE date=? AND currency=? AND rate=?`,
				startingDate, currency, rate); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT currency FROM fx_rates`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var currency string
		if err := rows.Scan(&currency); err != nil {
			rows.Close()
			return err
		}
		offered[currency] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for currency := range offered {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO meta (key, value) VALUES (?, ?)`,
			MetaRateSeeded(currency), startingDate); err != nil {
			return err
		}
	}
	return nil
}

// fillBudgetCurrency stamps every budget line with the reference.
func fillBudgetCurrency(ctx context.Context, tx *sql.Tx, _ Options) error {
	var reference string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, MetaRateReference).Scan(&reference); err != nil {
		return fmt.Errorf("reading the rate reference: %w", err)
	}
	_, err := tx.ExecContext(ctx, `UPDATE budgets SET currency=? WHERE currency=''`, reference)
	return err
}

// pinRateReference records the currency the rates are quoted against.
func pinRateReference(ctx context.Context, tx *sql.Tx, opts Options) error {
	ref := strings.ToUpper(strings.TrimSpace(opts.RateReference))
	if len(ref) != 3 {
		return fmt.Errorf("rate reference %q must be a three-letter currency code", opts.RateReference)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)`, MetaRateReference, ref)
	return err
}

// blankDerivedRates clears fx_rate on every row in the reference currency and
// on every row whose rate is what the table says for its date, so only rates
// entered by hand remain. The lookup is the one RateOn makes: the latest rate
// on or before the date, else the earliest on file.
func blankDerivedRates(ctx context.Context, tx *sql.Tx, _ Options) error {
	var reference string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, MetaRateReference).Scan(&reference); err != nil {
		return fmt.Errorf("reading the rate reference: %w", err)
	}
	type dated struct{ date, rate string }
	rates := map[string][]dated{} // by currency, oldest first
	rows, err := tx.QueryContext(ctx, `SELECT currency, date, rate FROM fx_rates ORDER BY date`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var currency, date, rate string
		if err := rows.Scan(&currency, &date, &rate); err != nil {
			rows.Close()
			return err
		}
		rates[currency] = append(rates[currency], dated{date, rate})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	tableRate := func(currency, date string) (string, bool) {
		filed := rates[currency]
		for i := len(filed) - 1; i >= 0; i-- {
			if filed[i].date <= date {
				return filed[i].rate, true
			}
		}
		if len(filed) > 0 {
			return filed[0].rate, true
		}
		return "", false
	}

	rows, err = tx.QueryContext(ctx, `SELECT id, currency, date, fx_rate FROM transactions WHERE currency <> ?`, reference)
	if err != nil {
		return err
	}
	var derived []any
	for rows.Next() {
		var id, currency, date, rate string
		if err := rows.Scan(&id, &currency, &date, &rate); err != nil {
			rows.Close()
			return err
		}
		if filed, ok := tableRate(currency, date); ok && money.Rate(rate).Equal(money.Rate(filed)) {
			derived = append(derived, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE transactions SET fx_rate='' WHERE currency=?`, reference); err != nil {
		return err
	}
	for _, id := range derived {
		if _, err := tx.ExecContext(ctx, `UPDATE transactions SET fx_rate='' WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}
