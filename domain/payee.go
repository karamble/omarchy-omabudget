package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/karamble/omarchy-omabudget/db"
)

// Payees, spec 1.5: who the money went to. A payee is made by naming one on a
// transaction, the way a tag is, and can then be renamed, merged, given
// aliases and a default category.

// Payee is one counterparty.
type Payee struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category string   `json:"defaultCategoryId,omitempty"`
	Notes    string   `json:"notes,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
	// Uses is how many live transactions name it.
	Uses int `json:"uses"`
}

// Payees lists them with their aliases and how often each is used, by name.
func (l *Ledger) Payees(ctx context.Context) ([]Payee, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT p.id, p.name, COALESCE(p.default_category_id,''), p.notes,
		(SELECT COUNT(*) FROM transactions t WHERE t.payee_id=p.id AND t.deleted_at IS NULL)
		FROM payees p WHERE p.deleted_at IS NULL ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Payee{}
	index := map[string]int{}
	for rows.Next() {
		var p Payee
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.Notes, &p.Uses); err != nil {
			return nil, err
		}
		index[p.ID] = len(out)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	alias, err := l.db.QueryContext(ctx, `SELECT payee_id, alias FROM payee_aliases ORDER BY alias`)
	if err != nil {
		return nil, err
	}
	defer alias.Close()
	for alias.Next() {
		var id, name string
		if err := alias.Scan(&id, &name); err != nil {
			return nil, err
		}
		if i, ok := index[id]; ok {
			out[i].Aliases = append(out[i].Aliases, name)
		}
	}
	return out, alias.Err()
}

// Payee finds one by id, by name or by one of its aliases.
func (l *Ledger) Payee(ctx context.Context, ref string) (Payee, error) {
	all, err := l.Payees(ctx)
	if err != nil {
		return Payee{}, err
	}
	ref = strings.TrimSpace(ref)
	for _, p := range all {
		if p.ID == ref || strings.EqualFold(p.Name, ref) {
			return p, nil
		}
	}
	for _, p := range all {
		for _, a := range p.Aliases {
			if strings.EqualFold(a, ref) {
				return p, nil
			}
		}
	}
	return Payee{}, fmt.Errorf("payee %q: %w", ref, db.ErrNotFound)
}

// ensurePayee resolves a name to an id inside a write, making the payee if it
// is new, exactly as a tag is made.
func ensurePayee(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM payees WHERE deleted_at IS NULL AND name=? COLLATE NOCASE`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	// An alias counts as the payee it belongs to.
	err = tx.QueryRowContext(ctx, `SELECT p.id FROM payee_aliases a JOIN payees p ON p.id=a.payee_id
		WHERE p.deleted_at IS NULL AND a.alias=? COLLATE NOCASE`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO payees (id,name,notes) VALUES (?,?,'')`, id, name); err != nil {
		return "", err
	}
	return id, nil
}

// UpdatePayee renames one and sets what it is usually booked to.
func (l *Ledger) UpdatePayee(ctx context.Context, p Payee) (Payee, error) {
	cur, err := l.Payee(ctx, p.ID)
	if err != nil {
		return Payee{}, err
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return Payee{}, errors.New("a payee needs a name")
	}
	all, err := l.Payees(ctx)
	if err != nil {
		return Payee{}, err
	}
	for _, other := range all {
		if other.ID != cur.ID && strings.EqualFold(other.Name, p.Name) {
			return Payee{}, fmt.Errorf("%s already exists: merge them instead", other.Name)
		}
	}
	if p.Category != "" {
		c, err := l.Category(ctx, p.Category)
		if err != nil {
			return Payee{}, err
		}
		p.Category = c.ID
	}
	if _, err := l.db.ExecContext(ctx, `UPDATE payees SET name=?, default_category_id=?, notes=? WHERE id=?`,
		p.Name, nullIf(p.Category), p.Notes, cur.ID); err != nil {
		return Payee{}, err
	}
	return l.Payee(ctx, cur.ID)
}

// AddAlias records another name the same payee goes by, which is what makes
// an import or a typo land on the right one.
func (l *Ledger) AddAlias(ctx context.Context, ref, alias string) (Payee, error) {
	p, err := l.Payee(ctx, ref)
	if err != nil {
		return Payee{}, err
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return Payee{}, errors.New("an alias needs a name")
	}
	if strings.EqualFold(alias, p.Name) {
		return Payee{}, fmt.Errorf("%s is already the name", alias)
	}
	if other, err := l.Payee(ctx, alias); err == nil && other.ID != p.ID {
		return Payee{}, fmt.Errorf("%s already belongs to %s", alias, other.Name)
	}
	if _, err := l.db.ExecContext(ctx, `INSERT OR IGNORE INTO payee_aliases (payee_id, alias) VALUES (?,?)`, p.ID, alias); err != nil {
		return Payee{}, err
	}
	return l.Payee(ctx, p.ID)
}

// RemoveAlias drops one.
func (l *Ledger) RemoveAlias(ctx context.Context, ref, alias string) (Payee, error) {
	p, err := l.Payee(ctx, ref)
	if err != nil {
		return Payee{}, err
	}
	res, err := l.db.ExecContext(ctx, `DELETE FROM payee_aliases WHERE payee_id=? AND alias=? COLLATE NOCASE`, p.ID, alias)
	if err != nil {
		return Payee{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Payee{}, fmt.Errorf("%s has no alias %q: %w", p.Name, alias, db.ErrNotFound)
	}
	return l.Payee(ctx, p.ID)
}

// MergePayees moves every transaction from one payee to another and keeps the
// old name as an alias, so the same statement line lands on the right one next
// time. It reports how many transactions moved.
func (l *Ledger) MergePayees(ctx context.Context, from, into string) (int, error) {
	a, err := l.Payee(ctx, from)
	if err != nil {
		return 0, err
	}
	b, err := l.Payee(ctx, into)
	if err != nil {
		return 0, err
	}
	if a.ID == b.ID {
		return 0, errors.New("that is the same payee")
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `UPDATE transactions SET payee_id=?, modified_at=? WHERE payee_id=?`, b.ID, l.stamp(), a.ID)
	if err != nil {
		return 0, err
	}
	moved, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE payee_aliases SET payee_id=? WHERE payee_id=?`, b.ID, a.ID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO payee_aliases (payee_id, alias) VALUES (?,?)`, b.ID, a.Name); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM payees WHERE id=?`, a.ID); err != nil {
		return 0, err
	}
	return int(moved), tx.Commit()
}

// RemovePayee deletes one nothing points at.
func (l *Ledger) RemovePayee(ctx context.Context, ref string) error {
	p, err := l.Payee(ctx, ref)
	if err != nil {
		return err
	}
	if p.Uses > 0 {
		return fmt.Errorf("%s is on %d transactions: merge it into another instead", p.Name, p.Uses)
	}
	var any int
	if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions WHERE payee_id=?`, p.ID).Scan(&any); err != nil {
		return err
	}
	if any > 0 {
		return fmt.Errorf("%s is on %d deleted transactions, which can still be restored", p.Name, any)
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM payee_aliases WHERE payee_id=?`, p.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM payees WHERE id=?`, p.ID); err != nil {
		return err
	}
	return tx.Commit()
}
