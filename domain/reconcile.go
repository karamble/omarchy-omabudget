package domain

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/karamble/omarchy-omabudget/money"
)

// Reconciliation is a statement held against the ledger: what the bank says
// the account held on a date, against what this ledger says, and the lines
// that are still waiting to be settled.
//
// A line is ticked by its status. Pending means it has not appeared on a
// statement yet; cleared means it has; reconciled means a statement it
// appeared on was settled and it is closed for good.
type Reconciliation struct {
	AccountID string `json:"accountId"`
	Account   string `json:"account"`
	Currency  string `json:"currency"`
	Through   string `json:"through"`
	// Statement is the closing balance the bank reports, in minor units.
	Statement int64 `json:"statement"`
	// Settled is everything already reconciled on or before Through, with
	// the opening balance, which is the floor this sheet starts from.
	Settled int64 `json:"settled"`
	// Ticked is the cleared lines in the window, the ones said to be on this
	// statement.
	Ticked int64 `json:"ticked"`
	// Difference is what the statement says less what the ledger says. Zero
	// is the only number that lets the sheet be settled.
	Difference int64 `json:"difference"`
	// Rows are the lines still waiting, oldest first.
	Rows []ReconcileLine `json:"rows"`
}

// ReconcileLine is one waiting line with the amount as this account sees it.
// The far leg of a transfer moves the other way, so the transaction's own
// amount is the wrong number to show on the receiving account's sheet.
type ReconcileLine struct {
	Transaction
	Signed int64 `json:"signed"`
}

// Balanced reports whether the sheet adds up.
func (r Reconciliation) Balanced() bool { return r.Difference == 0 }

// sumThrough adds both legs of everything on an account on or before a date
// whose status is in the given set.
func (l *Ledger) sumThrough(ctx context.Context, accountID, through string, statuses []Status) (int64, error) {
	if len(statuses) == 0 {
		return 0, nil
	}
	in := ""
	args := []any{accountID, through}
	for i, s := range statuses {
		if i > 0 {
			in += ","
		}
		in += "?"
		args = append(args, string(s))
	}
	// The counter leg is repeated with its own copy of the arguments.
	args = append(args, accountID, through)
	for _, s := range statuses {
		args = append(args, string(s))
	}
	q := `SELECT
		(SELECT COALESCE(SUM(amount),0) FROM transactions
		   WHERE account_id=? AND deleted_at IS NULL AND date<=? AND status IN (` + in + `)),
		(SELECT COALESCE(SUM(COALESCE(counter_amount, -amount)),0) FROM transactions
		   WHERE counter_account_id=? AND deleted_at IS NULL AND date<=? AND status IN (` + in + `))`
	var out, counter int64
	if err := l.db.QueryRowContext(ctx, q, args...).Scan(&out, &counter); err != nil {
		return 0, err
	}
	return out + counter, nil
}

// Reconcile builds the sheet for one account against a statement. It writes
// nothing; FinishReconcile is what settles it.
func (l *Ledger) Reconcile(ctx context.Context, accountRef, through string, statement int64) (Reconciliation, error) {
	a, err := l.Account(ctx, accountRef)
	if err != nil {
		return Reconciliation{}, err
	}
	if through == "" {
		through = l.now().Format(dateFmt)
	}
	if _, err := time.Parse(dateFmt, through); err != nil {
		return Reconciliation{}, fmt.Errorf("through %q must be YYYY-MM-DD", through)
	}
	settled, err := l.sumThrough(ctx, a.ID, through, []Status{Reconciled})
	if err != nil {
		return Reconciliation{}, err
	}
	settled += a.OpeningBalance
	ticked, err := l.sumThrough(ctx, a.ID, through, []Status{Cleared})
	if err != nil {
		return Reconciliation{}, err
	}
	rows, err := l.Transactions(ctx, Filter{AccountID: a.ID, To: through})
	if err != nil {
		return Reconciliation{}, err
	}
	waiting := []ReconcileLine{}
	for _, t := range rows {
		if t.Status == Reconciled {
			continue
		}
		signed := t.Amount
		if t.AccountID != a.ID {
			signed = -t.Amount
			if t.CounterAmount != nil {
				signed = *t.CounterAmount
			}
		}
		waiting = append(waiting, ReconcileLine{Transaction: t, Signed: signed})
	}
	// A statement reads oldest first.
	slices.Reverse(waiting)

	return Reconciliation{
		AccountID: a.ID, Account: a.Name, Currency: a.Currency, Through: through,
		Statement: statement, Settled: settled, Ticked: ticked,
		Difference: statement - (settled + ticked), Rows: waiting,
	}, nil
}

// FinishReconcile settles the sheet: every cleared line on or before the date
// becomes reconciled, and anything left pending waits for the next statement.
// It refuses while the sheet does not add up, because settling a sheet that
// is out is how a wrong balance becomes permanent.
func (l *Ledger) FinishReconcile(ctx context.Context, accountRef, through string, statement int64) (Reconciliation, int, error) {
	r, err := l.Reconcile(ctx, accountRef, through, statement)
	if err != nil {
		return Reconciliation{}, 0, err
	}
	if !r.Balanced() {
		return r, 0, fmt.Errorf("the sheet is out by %s: tick what is on the statement, or add what is missing",
			money.New(r.Difference, r.Currency).Format())
	}
	res, err := l.db.ExecContext(ctx, `UPDATE transactions SET status='reconciled', modified_at=?
		WHERE deleted_at IS NULL AND date<=? AND status='cleared'
		  AND (account_id=? OR counter_account_id=?)`,
		l.stamp(), through, r.AccountID, r.AccountID)
	if err != nil {
		return r, 0, err
	}
	n, _ := res.RowsAffected()
	r.Settled += r.Ticked
	r.Ticked = 0
	r.Rows = nil
	return r, int(n), nil
}

// SetStatus ticks a line on or off a statement, which is the one write a
// reconciliation sheet makes before it is settled.
func (l *Ledger) SetStatus(ctx context.Context, id string, s Status) (Transaction, error) {
	switch s {
	case Pending, Cleared, Reconciled:
	default:
		return Transaction{}, errors.New("status must be pending, cleared or reconciled")
	}
	t, err := l.Get(ctx, id)
	if err != nil {
		return Transaction{}, err
	}
	t.Status = s
	return l.Update(ctx, t)
}
