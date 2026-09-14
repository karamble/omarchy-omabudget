package db

import (
	"context"
	"database/sql"
)

// migration is one numbered, forward-only step. statements run in order inside
// a transaction; after runs in the same transaction for anything that needs
// Go rather than SQL, such as seeding.
type migration struct {
	version    int
	name       string
	statements []string
	after      func(ctx context.Context, tx *sql.Tx) error
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
}
