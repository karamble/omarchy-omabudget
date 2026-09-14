// Package domain is the ledger's model: accounts, categories, transactions
// and the rules spec section 11 says to enforce on every write.
package domain

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/money"
)

// Ledger is the domain over one open database.
type Ledger struct {
	db   *db.DB
	base string // base currency, spec section 8
	now  func() time.Time
}

// New wraps an open database. base is the household's base currency.
func New(d *db.DB, base string) *Ledger {
	return &Ledger{db: d, base: strings.ToUpper(base), now: time.Now}
}

// Base reports the base currency.
func (l *Ledger) Base() string { return l.base }

// DB exposes the database for maintenance such as backups.
func (l *Ledger) DB() *db.DB { return l.db }

// ---- accounts, spec 1.1

type AccountType string

const (
	Checking   AccountType = "checking"
	Savings    AccountType = "savings"
	Cash       AccountType = "cash"
	CreditCard AccountType = "credit_card"
	Loan       AccountType = "loan"
	Investment AccountType = "investment"
	Prepaid    AccountType = "prepaid"
	Receivable AccountType = "receivable"
	Payable    AccountType = "payable"
)

// Liability reports whether balances of this type count against net worth.
func (t AccountType) Liability() bool {
	return t == CreditCard || t == Loan || t == Payable
}

// Liquid reports whether the type counts toward liquid funds, spec 6.1.
func (t AccountType) Liquid() bool {
	return t == Checking || t == Cash || t == Savings
}

type Account struct {
	ID                string      `json:"id"`
	Name              string      `json:"name"`
	Type              AccountType `json:"type"`
	Currency          string      `json:"currency"`
	OpeningBalance    int64       `json:"openingBalance"`
	OpeningDate       string      `json:"openingDate"`
	Active            bool        `json:"active"`
	IncludeInNetWorth bool        `json:"includeInNetWorth"`
	Institution       string      `json:"institution,omitempty"`
	Last4             string      `json:"last4,omitempty"`
	SortOrder         int         `json:"sortOrder"`
	Colour            string      `json:"colour,omitempty"`
	Icon              string      `json:"icon,omitempty"`
	LowBalance        *int64      `json:"lowBalance,omitempty"`
}

var validTypes = map[AccountType]bool{
	Checking: true, Savings: true, Cash: true, CreditCard: true, Loan: true,
	Investment: true, Prepaid: true, Receivable: true, Payable: true,
}

func (a Account) validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("an account needs a name")
	}
	if !validTypes[a.Type] {
		return fmt.Errorf("unknown account type %q", a.Type)
	}
	if len(a.Currency) != 3 {
		return fmt.Errorf("currency %q must be a three-letter code", a.Currency)
	}
	if _, err := time.Parse("2006-01-02", a.OpeningDate); err != nil {
		return fmt.Errorf("opening date %q must be YYYY-MM-DD", a.OpeningDate)
	}
	return nil
}

func (l *Ledger) AddAccount(ctx context.Context, a Account) (Account, error) {
	a.Currency = strings.ToUpper(a.Currency)
	if a.OpeningDate == "" {
		a.OpeningDate = l.now().Format("2006-01-02")
	}
	if err := a.validate(); err != nil {
		return Account{}, err
	}
	if a.ID == "" {
		a.ID = newID()
	}
	a.Active = true
	ts := l.stamp()
	_, err := l.db.ExecContext(ctx, `INSERT INTO accounts
		(id,name,type,currency,opening_balance,opening_date,is_active,include_in_net_worth,
		 institution,identifier_last4,sort_order,colour,icon,low_balance,created_at,modified_at)
		VALUES (?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.Type, a.Currency, a.OpeningBalance, a.OpeningDate,
		boolInt(a.IncludeInNetWorth), a.Institution, a.Last4, a.SortOrder, a.Colour, a.Icon,
		a.LowBalance, ts, ts)
	if err != nil {
		return Account{}, err
	}
	return a, nil
}

func (l *Ledger) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id,name,type,currency,opening_balance,opening_date,
		is_active,include_in_net_worth,institution,identifier_last4,sort_order,colour,icon,low_balance
		FROM accounts WHERE deleted_at IS NULL ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var active, nw int
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &a.Currency, &a.OpeningBalance, &a.OpeningDate,
			&active, &nw, &a.Institution, &a.Last4, &a.SortOrder, &a.Colour, &a.Icon, &a.LowBalance); err != nil {
			return nil, err
		}
		a.Active, a.IncludeInNetWorth = active == 1, nw == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

func (l *Ledger) Account(ctx context.Context, id string) (Account, error) {
	all, err := l.Accounts(ctx)
	if err != nil {
		return Account{}, err
	}
	for _, a := range all {
		if a.ID == id || strings.EqualFold(a.Name, id) {
			return a, nil
		}
	}
	return Account{}, fmt.Errorf("account %q: %w", id, db.ErrNotFound)
}

// UpdateAccount writes an account back by id. Type and currency are fixed
// once the account has postings, since its balance is kept in that currency.
func (l *Ledger) UpdateAccount(ctx context.Context, a Account) (Account, error) {
	cur, err := l.Account(ctx, a.ID)
	if err != nil {
		return Account{}, err
	}
	a.ID = cur.ID
	a.Currency = strings.ToUpper(a.Currency)
	if a.OpeningDate == "" {
		a.OpeningDate = cur.OpeningDate
	}
	if err := a.validate(); err != nil {
		return Account{}, err
	}
	if a.Type != cur.Type || a.Currency != cur.Currency {
		var n int
		if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions
			WHERE (account_id=? OR counter_account_id=?) AND deleted_at IS NULL`, cur.ID, cur.ID).Scan(&n); err != nil {
			return Account{}, err
		}
		if n > 0 {
			return Account{}, errors.New("type and currency are fixed once an account has postings")
		}
	}
	_, err = l.db.ExecContext(ctx, `UPDATE accounts SET name=?,type=?,currency=?,opening_balance=?,opening_date=?,
		is_active=?,include_in_net_worth=?,institution=?,identifier_last4=?,sort_order=?,colour=?,icon=?,
		low_balance=?,modified_at=? WHERE id=?`,
		a.Name, a.Type, a.Currency, a.OpeningBalance, a.OpeningDate, boolInt(a.Active), boolInt(a.IncludeInNetWorth),
		a.Institution, a.Last4, a.SortOrder, a.Colour, a.Icon, a.LowBalance, l.stamp(), a.ID)
	if err != nil {
		return Account{}, err
	}
	return a, nil
}

// Balance is opening balance plus every non-deleted posting on the account,
// in the account's own currency. Transfers count on both legs.
func (l *Ledger) Balance(ctx context.Context, accountID string) (money.Amount, error) {
	a, err := l.Account(ctx, accountID)
	if err != nil {
		return money.Amount{}, err
	}
	var out, in sql.NullInt64
	err = l.db.QueryRowContext(ctx, `
		SELECT
		  (SELECT COALESCE(SUM(amount),0) FROM transactions
		     WHERE account_id=? AND deleted_at IS NULL),
		  (SELECT COALESCE(SUM(COALESCE(counter_amount, -amount)),0) FROM transactions
		     WHERE counter_account_id=? AND deleted_at IS NULL)`,
		a.ID, a.ID).Scan(&out, &in)
	if err != nil {
		return money.Amount{}, err
	}
	return money.New(a.OpeningBalance+out.Int64+in.Int64, a.Currency), nil
}

// ---- categories, spec 1.4

type Category struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ParentID   string `json:"parentId,omitempty"`
	Kind       string `json:"kind"`
	Icon       string `json:"icon,omitempty"`
	System     bool   `json:"system"`
	Archived   bool   `json:"archived"`
	Behaviour  string `json:"behaviour"`
	Excluded   bool   `json:"excludedFromStatistics"`
	SortOrder  int    `json:"sortOrder"`
	ParentName string `json:"parentName,omitempty"`

	// A goal category saves toward GoalTarget by the month GoalDue, spec 5.3.
	GoalTarget int64  `json:"goalTarget,omitempty"`
	GoalDue    string `json:"goalDue,omitempty"`

	// Recent is how many transactions have been booked to it in the last
	// ninety days, which is what a picker sorts the likely answers by.
	Recent int `json:"recent"`
}

func (l *Ledger) Categories(ctx context.Context) ([]Category, error) {
	since := l.now().AddDate(0, 0, -90).Format(dateFmt)
	rows, err := l.db.QueryContext(ctx, `SELECT c.id,c.name,COALESCE(c.parent_id,''),c.kind,c.icon,
		c.is_system,c.is_archived,c.default_budget_behaviour,c.exclude_from_statistics,c.sort_order,
		COALESCE(p.name,''),c.goal_target,c.goal_due,
		(SELECT COUNT(*) FROM transactions t
		   WHERE t.category_id=c.id AND t.deleted_at IS NULL AND t.date>=?)
		+ (SELECT COUNT(*) FROM splits s JOIN transactions t ON t.id=s.transaction_id
		   WHERE s.category_id=c.id AND t.deleted_at IS NULL AND t.date>=?)
		FROM categories c LEFT JOIN categories p ON p.id=c.parent_id
		WHERE c.deleted_at IS NULL
		ORDER BY COALESCE(p.sort_order, c.sort_order), c.parent_id IS NOT NULL, c.sort_order, c.name`,
		since, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		var sys, arch, excl int
		if err := rows.Scan(&c.ID, &c.Name, &c.ParentID, &c.Kind, &c.Icon, &sys, &arch, &c.Behaviour,
			&excl, &c.SortOrder, &c.ParentName, &c.GoalTarget, &c.GoalDue, &c.Recent); err != nil {
			return nil, err
		}
		c.System, c.Archived, c.Excluded = sys == 1, arch == 1, excl == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// Category finds one by id, by name matched against the leaf or its
// "Parent / Child" path, or, failing that, by a substring of a leaf name, all
// case-insensitively, so quick-add takes what a person types: "coffee" finds
// "Coffee & snacks". An exact match always wins over a partial one, and a
// partial match that fits several categories is reported as ambiguous rather
// than guessed.
func (l *Ledger) Category(ctx context.Context, ref string) (Category, error) {
	all, err := l.Categories(ctx)
	if err != nil {
		return Category{}, err
	}
	return matchCategory(all, ref, false)
}

// ---- transactions, spec 1.2, 1.3, 4.1, 4.3

type Kind string

const (
	Expense  Kind = "expense"
	Income   Kind = "income"
	Transfer Kind = "transfer"
)

type Status string

const (
	Pending    Status = "pending"
	Cleared    Status = "cleared"
	Reconciled Status = "reconciled"
)

// Split is one line of a split transaction, spec 1.3.
type Split struct {
	ID         string `json:"id,omitempty"`
	CategoryID string `json:"categoryId"`
	Amount     int64  `json:"amount"`
	BaseAmount int64  `json:"baseAmount"`
	Note       string `json:"note,omitempty"`
}

type Transaction struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	Date string `json:"date"`

	// Amount is signed from the account's point of view: negative leaves it.
	Amount     int64      `json:"amount"`
	Currency   string     `json:"currency"`
	FXRate     money.Rate `json:"fxRate"`
	BaseAmount int64      `json:"baseAmount"`

	AccountID        string `json:"accountId"`
	CounterAccountID string `json:"counterAccountId,omitempty"`
	// CounterAmount is set on a cross-currency transfer: both legs as entered,
	// spec 4.3. Nil means the same amount, negated.
	CounterAmount *int64 `json:"counterAmount,omitempty"`

	CategoryID string `json:"categoryId,omitempty"`
	PayeeID    string `json:"payeeId,omitempty"`
	// PayeeName is who it was paid to. Naming one that does not exist yet
	// makes it, the way a tag is made.
	PayeeName   string   `json:"payee,omitempty"`
	Description string   `json:"description,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	Status      Status   `json:"status"`
	Tags        []string `json:"tags,omitempty"`
	Splits      []Split  `json:"splits,omitempty"`

	CreatedAt  string `json:"createdAt,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	DeletedAt  string `json:"deletedAt,omitempty"`

	// Set on instances a recurring rule posted.
	RecurringRuleID     string `json:"recurringRuleId,omitempty"`
	IsRecurringInstance bool   `json:"isRecurringInstance,omitempty"`
}

// Add is the write path for every kind of transaction. It fills what the
// caller left blank (date, currency from the account, base amount from the
// rate) and enforces the spec 11 rules that block a save.
func (l *Ledger) Add(ctx context.Context, t Transaction) (Transaction, error) {
	if err := l.prepare(ctx, &t); err != nil {
		return Transaction{}, err
	}
	if t.ID == "" {
		t.ID = newID()
	}
	ts := l.stamp()
	t.CreatedAt, t.ModifiedAt = ts, ts

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback()

	if t.PayeeID, err = ensurePayee(ctx, tx, t.PayeeName); err != nil {
		return Transaction{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO transactions
		(id,kind,date,amount,currency,fx_rate,base_amount,account_id,counter_account_id,counter_amount,
		 category_id,payee_id,description,notes,status,recurring_rule_id,is_recurring_instance,created_at,modified_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Kind, t.Date, t.Amount, t.Currency, string(t.FXRate), t.BaseAmount, t.AccountID,
		nullIf(t.CounterAccountID), t.CounterAmount, nullIf(t.CategoryID), nullIf(t.PayeeID),
		t.Description, t.Notes, t.Status, nullIf(t.RecurringRuleID), boolInt(t.IsRecurringInstance), ts, ts)
	if err != nil {
		return Transaction{}, err
	}
	if err := l.writeLines(ctx, tx, &t); err != nil {
		return Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return Transaction{}, err
	}
	return t, nil
}

// prepare resolves references, normalises signs and freezes the base amount:
// everything a transaction needs before it is written, new or edited.
func (l *Ledger) prepare(ctx context.Context, t *Transaction) error {
	acct, err := l.Account(ctx, t.AccountID)
	if err != nil {
		return err
	}
	t.AccountID = acct.ID
	if t.Currency == "" {
		t.Currency = acct.Currency
	}
	t.Currency = strings.ToUpper(t.Currency)
	if t.Date == "" {
		t.Date = l.now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", t.Date); err != nil {
		return fmt.Errorf("date %q must be YYYY-MM-DD", t.Date)
	}
	if t.Amount == 0 {
		return money.ErrZero
	}
	if t.Status == "" {
		t.Status = Cleared
	}

	switch t.Kind {
	case Expense, Income:
		if err := l.checkCategory(ctx, t); err != nil {
			return err
		}
		if t.Kind == Expense && t.Amount > 0 {
			t.Amount = -t.Amount
		}
		if t.Kind == Income && t.Amount < 0 {
			t.Amount = -t.Amount
		}
		t.CounterAccountID, t.CounterAmount = "", nil
	case Transfer:
		if t.CategoryID != "" {
			// Spec 11: strip silently, warn once. The API layer reports it;
			// the ledger just does not store it.
			t.CategoryID = ""
		}
		if t.CounterAccountID == "" {
			return errors.New("a transfer needs a destination account")
		}
		counter, err := l.Account(ctx, t.CounterAccountID)
		if err != nil {
			return err
		}
		t.CounterAccountID = counter.ID
		if counter.ID == acct.ID {
			return errors.New("a transfer needs two different accounts")
		}
		if t.Amount > 0 {
			t.Amount = -t.Amount
		}
		if counter.Currency != acct.Currency && t.CounterAmount == nil {
			return fmt.Errorf("a transfer from %s to %s needs the amount received", acct.Currency, counter.Currency)
		}
		if counter.Currency == acct.Currency {
			t.CounterAmount = nil
		}
	default:
		return fmt.Errorf("unknown transaction kind %q", t.Kind)
	}

	if err := l.freezeBase(ctx, t); err != nil {
		return err
	}
	return l.checkSplits(t)
}

// writeLines stores the split lines and tags of a transaction whose row is
// already written.
func (l *Ledger) writeLines(ctx context.Context, tx *sql.Tx, t *Transaction) error {
	for i := range t.Splits {
		s := &t.Splits[i]
		if s.ID == "" {
			s.ID = newID()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO splits (id,transaction_id,category_id,amount,base_amount,note,sort_order)
			VALUES (?,?,?,?,?,?,?)`, s.ID, t.ID, s.CategoryID, s.Amount, s.BaseAmount, s.Note, i); err != nil {
			return err
		}
	}
	for _, name := range t.Tags {
		tagID, err := ensureTag(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO transaction_tags (transaction_id, tag_id) VALUES (?,?)`, t.ID, tagID); err != nil {
			return err
		}
	}
	return nil
}

// checkCategory resolves the category and enforces "kind must match
// direction" (spec 11), since an income category on an expense is a mistake
// rather than a choice.
func (l *Ledger) checkCategory(ctx context.Context, t *Transaction) error {
	if t.CategoryID == "" {
		if len(t.Splits) > 0 {
			return nil
		}
		t.CategoryID = "sys-uncategorised"
		return nil
	}
	c, err := l.Category(ctx, t.CategoryID)
	if err != nil {
		return err
	}
	t.CategoryID = c.ID
	if !c.System && c.Kind != string(t.Kind) {
		return fmt.Errorf("%s is an %s category, but this is %s", c.Name, c.Kind, t.Kind)
	}
	return nil
}

// freezeBase computes base_amount once, from the rate at entry, spec 8. A
// transaction in the base currency has rate 1. Otherwise the caller's rate
// wins, then the fx_rates table for that date, and with neither the save is
// refused rather than guessed.
func (l *Ledger) freezeBase(ctx context.Context, t *Transaction) error {
	if t.Currency == l.base {
		t.FXRate = "1"
		t.BaseAmount = t.Amount
		return nil
	}
	if t.FXRate == "" {
		var rate string
		err := l.db.QueryRowContext(ctx,
			`SELECT rate FROM fx_rates WHERE currency=? AND date<=? ORDER BY date DESC LIMIT 1`,
			t.Currency, t.Date).Scan(&rate)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("no %s to %s rate known for %s: give one, or add it to the rate table", t.Currency, l.base, t.Date)
		}
		if err != nil {
			return err
		}
		t.FXRate = money.Rate(rate)
	}
	base, err := money.Convert(money.New(t.Amount, t.Currency), t.FXRate, l.base)
	if err != nil {
		return err
	}
	t.BaseAmount = base.Minor
	return nil
}

// checkSplits enforces spec 1.3: lines sum to the parent, exactly, and each
// line carries its own frozen base amount in proportion.
func (l *Ledger) checkSplits(t *Transaction) error {
	if len(t.Splits) == 0 {
		return nil
	}
	if t.Kind == Transfer {
		return errors.New("a transfer cannot be split")
	}
	var sum int64
	for i := range t.Splits {
		s := &t.Splits[i]
		if s.CategoryID == "" {
			return fmt.Errorf("split line %d needs a category", i+1)
		}
		if s.Amount == 0 {
			return fmt.Errorf("split line %d is zero", i+1)
		}
		// Lines carry the parent's sign.
		if (t.Amount < 0) != (s.Amount < 0) {
			s.Amount = -s.Amount
		}
		sum += s.Amount
		base, err := money.Convert(money.New(s.Amount, t.Currency), t.FXRate, l.base)
		if err != nil {
			return err
		}
		s.BaseAmount = base.Minor
	}
	if sum != t.Amount {
		remainder := money.New(t.Amount-sum, t.Currency)
		return fmt.Errorf("split lines sum to %s, %s short of the total", money.New(sum, t.Currency).Format(), remainder.Format())
	}
	return nil
}

// Filter narrows a transaction listing. Live rows are listed unless Deleted
// asks for the soft-deleted ones, or ID names one row whatever its state.
type Filter struct {
	ID string
	// AccountID matches either leg of a transfer.
	AccountID string
	// CategoryID matches the category, the categories under it, and split
	// lines booked to either.
	CategoryID string
	PayeeID    string
	// Tags matches a transaction carrying any of them.
	Tags     []string
	Kind     Kind
	Status   Status
	Search   string // matched against description, notes and payee name
	From, To string // inclusive dates
	// Min and Max bound the amount, ignoring its sign, in base currency.
	Min, Max int64
	Deleted  bool
	Limit    int
	Offset   int
}

func (l *Ledger) Transactions(ctx context.Context, f Filter) ([]Transaction, error) {
	q := `SELECT t.id,t.kind,t.date,t.amount,t.currency,t.fx_rate,t.base_amount,t.account_id,
		COALESCE(t.counter_account_id,''),t.counter_amount,COALESCE(t.category_id,''),COALESCE(t.payee_id,''),
		t.description,t.notes,t.status,t.created_at,t.modified_at,COALESCE(t.deleted_at,''),
		COALESCE(t.recurring_rule_id,''),t.is_recurring_instance,COALESCE(p.name,'')
		FROM transactions t LEFT JOIN payees p ON p.id=t.payee_id WHERE 1=1`
	var args []any
	switch {
	case f.ID != "":
		q += ` AND t.id=?`
		args = append(args, f.ID)
	case f.Deleted:
		q += ` AND t.deleted_at IS NOT NULL`
	default:
		q += ` AND t.deleted_at IS NULL`
	}
	if f.AccountID != "" {
		q += ` AND (t.account_id=? OR t.counter_account_id=?)`
		args = append(args, f.AccountID, f.AccountID)
	}
	if f.CategoryID != "" {
		// A group stands for the categories under it, or drilling into one
		// from a report would show only what was booked on the group itself.
		q += ` AND (t.category_id=? OR t.category_id IN (SELECT id FROM categories WHERE parent_id=?)
			OR t.id IN (SELECT s.transaction_id FROM splits s LEFT JOIN categories c ON c.id=s.category_id
			            WHERE s.category_id=? OR c.parent_id=?))`
		args = append(args, f.CategoryID, f.CategoryID, f.CategoryID, f.CategoryID)
	}
	if f.PayeeID != "" {
		q += ` AND t.payee_id=?`
		args = append(args, f.PayeeID)
	}
	if len(f.Tags) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(f.Tags)), ",")
		q += ` AND t.id IN (SELECT tt.transaction_id FROM transaction_tags tt JOIN tags g ON g.id=tt.tag_id
			WHERE g.name COLLATE NOCASE IN (` + marks + `))`
		for _, tag := range f.Tags {
			args = append(args, tag)
		}
	}
	if f.Min > 0 {
		q += ` AND ABS(t.base_amount)>=?`
		args = append(args, f.Min)
	}
	if f.Max > 0 {
		q += ` AND ABS(t.base_amount)<=?`
		args = append(args, f.Max)
	}
	if f.Kind != "" {
		q += ` AND t.kind=?`
		args = append(args, f.Kind)
	}
	if f.Status != "" {
		q += ` AND t.status=?`
		args = append(args, f.Status)
	}
	if f.Search != "" {
		like := "%" + f.Search + "%"
		q += ` AND (t.description LIKE ? OR t.notes LIKE ? OR COALESCE(p.name,'') LIKE ?)`
		args = append(args, like, like, like)
	}
	if f.From != "" {
		q += ` AND t.date>=?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND t.date<=?`
		args = append(args, f.To)
	}
	q += ` ORDER BY t.date DESC, t.created_at DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
		if f.Offset > 0 {
			q += fmt.Sprintf(` OFFSET %d`, f.Offset)
		}
	}
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transaction
	for rows.Next() {
		var t Transaction
		var rate string
		var instance int
		if err := rows.Scan(&t.ID, &t.Kind, &t.Date, &t.Amount, &t.Currency, &rate, &t.BaseAmount,
			&t.AccountID, &t.CounterAccountID, &t.CounterAmount, &t.CategoryID, &t.PayeeID,
			&t.Description, &t.Notes, &t.Status, &t.CreatedAt, &t.ModifiedAt, &t.DeletedAt,
			&t.RecurringRuleID, &instance, &t.PayeeName); err != nil {
			return nil, err
		}
		t.FXRate = money.Rate(rate)
		t.IsRecurringInstance = instance == 1
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := l.attach(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// SoftDelete marks a transaction deleted, spec P5: undo must exist.
func (l *Ledger) SoftDelete(ctx context.Context, id string) error {
	res, err := l.db.ExecContext(ctx, `UPDATE transactions SET deleted_at=?, modified_at=? WHERE id=? AND deleted_at IS NULL`,
		l.stamp(), l.stamp(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("transaction %q: %w", id, db.ErrNotFound)
	}
	return nil
}

// Restore undoes a soft delete.
func (l *Ledger) Restore(ctx context.Context, id string) error {
	res, err := l.db.ExecContext(ctx, `UPDATE transactions SET deleted_at=NULL, modified_at=? WHERE id=? AND deleted_at IS NOT NULL`,
		l.stamp(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("transaction %q: %w", id, db.ErrNotFound)
	}
	return nil
}

// ---- helpers

func ensureTag(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "#")
	if name == "" {
		return "", errors.New("empty tag")
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE name=? AND deleted_at IS NULL`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO tags (id, name) VALUES (?,?)`, id, name); err != nil {
		return "", err
	}
	return id, nil
}

func (l *Ledger) stamp() string { return l.now().UTC().Format(time.RFC3339) }

func newID() string {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// accountHolds says, in words, what still points at an account, or "" when
// nothing does. Same shape as categoryHolds.
func (l *Ledger) accountHolds(ctx context.Context, a Account) (string, error) {
	var n int
	if err := l.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transactions WHERE account_id=? OR counter_account_id=?`,
		a.ID, a.ID).Scan(&n); err != nil {
		return "", err
	}
	switch {
	case n == 1:
		return "has a transaction", nil
	case n > 1:
		return fmt.Sprintf("has %d transactions", n), nil
	}

	// Rules keep their transaction as JSON, so they have to be read.
	rows, err := l.db.QueryContext(ctx, `SELECT name, template FROM recurring_rules WHERE deleted_at IS NULL`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return "", err
		}
		var t Template
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			continue
		}
		if t.AccountID == a.ID || t.CounterAccountID == a.ID {
			return fmt.Sprintf("is what %s posts to", name), nil
		}
	}
	return "", rows.Err()
}

// RemoveAccount deletes an account nothing points at, which is what makes a
// typo at creation fixable. Anything with history is closed, not removed.
func (l *Ledger) RemoveAccount(ctx context.Context, ref string) error {
	a, err := l.Account(ctx, ref)
	if err != nil {
		return err
	}
	held, err := l.accountHolds(ctx, a)
	if err != nil {
		return err
	}
	if held != "" {
		return fmt.Errorf("%s %s: close it instead, which keeps the history and takes it out of the pickers", a.Name, held)
	}
	_, err = l.db.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, a.ID)
	return err
}
