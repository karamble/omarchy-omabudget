package domain

import (
	"context"
	"fmt"
	"strings"

	"github.com/karamble/omarchy-omabudget/db"
)

// Get returns one transaction with its split lines and tags, deleted or not.
func (l *Ledger) Get(ctx context.Context, id string) (Transaction, error) {
	list, err := l.Transactions(ctx, Filter{ID: id})
	if err != nil {
		return Transaction{}, err
	}
	if len(list) == 0 {
		return Transaction{}, fmt.Errorf("transaction %q: %w", id, db.ErrNotFound)
	}
	return list[0], nil
}

// Update rewrites a transaction from t, whose id names the row. Callers start
// from Get and change what they need: every field is written back and the
// split lines and tags are replaced. The creation time and the rate frozen at
// entry stay, so an edited amount converts at the original rate.
func (l *Ledger) Update(ctx context.Context, t Transaction) (Transaction, error) {
	cur, err := l.Get(ctx, t.ID)
	if err != nil {
		return Transaction{}, err
	}
	if cur.DeletedAt != "" {
		return Transaction{}, fmt.Errorf("transaction %s is deleted: restore it first", t.ID)
	}
	if t.FXRate == "" {
		t.FXRate = cur.FXRate
	}
	if err := l.prepare(ctx, &t); err != nil {
		return Transaction{}, err
	}
	t.CreatedAt = cur.CreatedAt
	t.ModifiedAt = l.stamp()

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback()

	if t.PayeeID, err = ensurePayee(ctx, tx, t.PayeeName); err != nil {
		return Transaction{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE transactions SET kind=?,date=?,amount=?,currency=?,fx_rate=?,base_amount=?,
		account_id=?,counter_account_id=?,counter_amount=?,category_id=?,payee_id=?,description=?,notes=?,
		status=?,modified_at=? WHERE id=?`,
		t.Kind, t.Date, t.Amount, t.Currency, string(t.FXRate), t.BaseAmount, t.AccountID,
		nullIf(t.CounterAccountID), t.CounterAmount, nullIf(t.CategoryID), nullIf(t.PayeeID),
		t.Description, t.Notes, t.Status, t.ModifiedAt, t.ID)
	if err != nil {
		return Transaction{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM splits WHERE transaction_id=?`, t.ID); err != nil {
		return Transaction{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM transaction_tags WHERE transaction_id=?`, t.ID); err != nil {
		return Transaction{}, err
	}
	for i := range t.Splits {
		t.Splits[i].ID = ""
	}
	if err := l.writeLines(ctx, tx, &t); err != nil {
		return Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return Transaction{}, err
	}
	return t, nil
}

// attach loads the split lines and tags of a listing in two queries per
// chunk of ids rather than two per row.
func (l *Ledger) attach(ctx context.Context, list []Transaction) error {
	const chunk = 500
	idx := make(map[string]int, len(list))
	for i := range list {
		idx[list[i].ID] = i
	}
	for start := 0; start < len(list); start += chunk {
		end := start + chunk
		if end > len(list) {
			end = len(list)
		}
		ids := make([]any, 0, end-start)
		for _, t := range list[start:end] {
			ids = append(ids, t.ID)
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")

		rows, err := l.db.QueryContext(ctx, `SELECT id,transaction_id,category_id,amount,base_amount,note
			FROM splits WHERE transaction_id IN (`+marks+`) ORDER BY transaction_id, sort_order`, ids...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s Split
			var tid string
			if err := rows.Scan(&s.ID, &tid, &s.CategoryID, &s.Amount, &s.BaseAmount, &s.Note); err != nil {
				rows.Close()
				return err
			}
			list[idx[tid]].Splits = append(list[idx[tid]].Splits, s)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		rows, err = l.db.QueryContext(ctx, `SELECT tt.transaction_id, t.name FROM transaction_tags tt
			JOIN tags t ON t.id=tt.tag_id WHERE tt.transaction_id IN (`+marks+`) ORDER BY t.name`, ids...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var tid, name string
			if err := rows.Scan(&tid, &name); err != nil {
				rows.Close()
				return err
			}
			list[idx[tid]].Tags = append(list[idx[tid]].Tags, name)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// SweepDeleted removes transactions deleted before the stamp given, which is
// the end of the recycle bin's thirty days.
func (l *Ledger) SweepDeleted(ctx context.Context, before string) (int64, error) {
	res, err := l.db.ExecContext(ctx, `DELETE FROM transactions WHERE deleted_at IS NOT NULL AND deleted_at < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
