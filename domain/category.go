package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/karamble/omarchy-omabudget/db"
)

// The taxonomy is two levels, spec 2: groups and their children. A child's id
// is its parent's id and its own slug, which is how the seed builds them, so a
// rename never moves history: the id stays, only the name column changes.

// matchCategory picks the category a reference names: an id, a name, a
// "Parent / Child" path, or a substring of a name, case-insensitively. An
// exact match wins over a partial one and an ambiguous partial is reported
// rather than guessed. Archived categories are skipped unless asked for, so a
// picker never offers one while a management verb can still reach it.
func matchCategory(all []Category, ref string, archived bool) (Category, error) {
	ref = strings.TrimSpace(ref)
	for _, c := range all {
		if c.ID == ref {
			return c, nil
		}
	}
	pathOf := func(c Category) string {
		if c.ParentName != "" {
			return c.ParentName + " / " + c.Name
		}
		return c.Name
	}
	var exact, partial []Category
	needle := strings.ToLower(ref)
	for _, c := range all {
		if (c.Archived && !archived) || c.ID == "sys-transfer" {
			continue
		}
		switch {
		case strings.EqualFold(c.Name, ref), strings.EqualFold(pathOf(c), ref):
			exact = append(exact, c)
		case needle != "" && strings.Contains(strings.ToLower(c.Name), needle):
			partial = append(partial, c)
		}
	}
	hits := exact
	if len(hits) == 0 {
		hits = partial
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Category{}, fmt.Errorf("category %q: %w", ref, db.ErrNotFound)
	}
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, pathOf(h))
	}
	return Category{}, fmt.Errorf("category %q is ambiguous: %s", ref, strings.Join(names, ", "))
}

// CategoryAny resolves a reference the way Category does and also finds
// archived ones, which the management verbs need.
func (l *Ledger) CategoryAny(ctx context.Context, ref string) (Category, error) {
	all, err := l.Categories(ctx)
	if err != nil {
		return Category{}, err
	}
	return matchCategory(all, ref, true)
}

// AddCategory creates one. A parent makes it a child and lends it its kind;
// without a parent it is a group of its own.
func (l *Ledger) AddCategory(ctx context.Context, c Category) (Category, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return Category{}, errors.New("a category needs a name")
	}
	all, err := l.Categories(ctx)
	if err != nil {
		return Category{}, err
	}

	base := slug(c.Name)
	if c.ParentID != "" {
		parent, err := matchCategory(all, c.ParentID, false)
		if err != nil {
			return Category{}, err
		}
		if parent.System {
			return Category{}, fmt.Errorf("%s is a system category and takes no children", parent.Name)
		}
		if parent.ParentID != "" {
			return Category{}, fmt.Errorf("%s is already a child of %s: the taxonomy is two levels deep", parent.Name, parent.ParentName)
		}
		c.ParentID = parent.ID
		c.Kind = parent.Kind
		base = parent.ID + "/" + base
	}
	if base == "" || base == "/" {
		return Category{}, fmt.Errorf("a name needs letters or digits: %q makes no id", c.Name)
	}

	c.Kind = strings.ToLower(strings.TrimSpace(c.Kind))
	if c.Kind == "" {
		c.Kind = string(Expense)
	}
	if c.Kind != string(Expense) && c.Kind != string(Income) {
		return Category{}, fmt.Errorf("kind %q: expense or income", c.Kind)
	}
	c.Behaviour = strings.ToLower(strings.TrimSpace(c.Behaviour))
	if c.Behaviour == "" {
		c.Behaviour = BehaviourMonthly
	}
	if !behaviours[c.Behaviour] {
		return Category{}, fmt.Errorf("unknown behaviour %q: monthly, rollover, goal or untracked", c.Behaviour)
	}
	if c.Behaviour == BehaviourGoal {
		return Category{}, errors.New("a goal needs a target: set the behaviour after the category exists")
	}

	last := 0
	for _, x := range all {
		if x.ParentID != c.ParentID {
			continue
		}
		if strings.EqualFold(x.Name, c.Name) {
			return Category{}, fmt.Errorf("%s already exists", pathOfCategory(x))
		}
		if x.SortOrder > last {
			last = x.SortOrder
		}
	}
	if c.SortOrder == 0 {
		c.SortOrder = last + 1
	}

	// Two names can slug to the same id under one parent only through
	// punctuation; number the later one rather than refuse it.
	id := base
	for n := 2; ; n++ {
		taken, err := l.categoryIDTaken(ctx, id)
		if err != nil {
			return Category{}, err
		}
		if !taken {
			break
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
	c.ID = id
	c.System, c.Archived = false, false

	if _, err := l.db.ExecContext(ctx, `INSERT INTO categories
		(id,name,parent_id,kind,icon,colour,is_archived,is_system,default_budget_behaviour,
		 exclude_from_statistics,sort_order)
		VALUES (?,?,?,?,?,'',0,0,?,?,?)`,
		c.ID, c.Name, nullIf(c.ParentID), c.Kind, c.Icon, c.Behaviour, boolInt(c.Excluded), c.SortOrder); err != nil {
		return Category{}, err
	}
	// A child inherits its parent's statistics flag unless it was asked for.
	if c.ParentID != "" && !c.Excluded {
		parent, err := matchCategory(all, c.ParentID, true)
		if err == nil && parent.Excluded {
			if _, err := l.db.ExecContext(ctx, `UPDATE categories SET exclude_from_statistics=1 WHERE id=?`, c.ID); err != nil {
				return Category{}, err
			}
			c.Excluded = true
		}
	}
	return l.CategoryAny(ctx, c.ID)
}

// UpdateCategory writes a category's own fields back: what it is called, its
// glyph, where it sorts, whether statistics skip it and whether it is
// archived. Kind and parent do not move, and behaviour and goal have their
// own verbs.
func (l *Ledger) UpdateCategory(ctx context.Context, c Category) (Category, error) {
	cur, err := l.CategoryAny(ctx, c.ID)
	if err != nil {
		return Category{}, err
	}
	if cur.System {
		return Category{}, fmt.Errorf("%s is a system category and cannot be edited", cur.Name)
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return Category{}, errors.New("a category needs a name")
	}
	all, err := l.Categories(ctx)
	if err != nil {
		return Category{}, err
	}
	for _, x := range all {
		if x.ID != cur.ID && x.ParentID == cur.ParentID && strings.EqualFold(x.Name, c.Name) {
			return Category{}, fmt.Errorf("%s already exists", pathOfCategory(x))
		}
	}
	if c.SortOrder == 0 {
		c.SortOrder = cur.SortOrder
	}

	if _, err := l.db.ExecContext(ctx, `UPDATE categories
		SET name=?,icon=?,sort_order=?,exclude_from_statistics=?,is_archived=? WHERE id=?`,
		c.Name, c.Icon, c.SortOrder, boolInt(c.Excluded), boolInt(c.Archived), cur.ID); err != nil {
		return Category{}, err
	}

	// Transactions are booked on children, so a group's flags mean nothing
	// unless they reach them: an archived group must take its children out of
	// the pickers, and a group outside statistics must take them out too.
	if cur.ParentID == "" {
		if c.Excluded != cur.Excluded {
			if _, err := l.db.ExecContext(ctx, `UPDATE categories SET exclude_from_statistics=? WHERE parent_id=?`,
				boolInt(c.Excluded), cur.ID); err != nil {
				return Category{}, err
			}
		}
		if c.Archived != cur.Archived {
			if _, err := l.db.ExecContext(ctx, `UPDATE categories SET is_archived=? WHERE parent_id=?`,
				boolInt(c.Archived), cur.ID); err != nil {
				return Category{}, err
			}
		}
	}
	return l.CategoryAny(ctx, cur.ID)
}

// ArchiveCategory takes a category out of the pickers, or puts it back,
// without touching a single transaction.
func (l *Ledger) ArchiveCategory(ctx context.Context, ref string, archived bool) (Category, error) {
	cur, err := l.CategoryAny(ctx, ref)
	if err != nil {
		return Category{}, err
	}
	cur.Archived = archived
	return l.UpdateCategory(ctx, cur)
}

// RemoveCategory deletes one outright, and only one nothing points at.
// Anything with history behind it is kept and the caller is told to archive
// it instead, because deleting it would orphan a transaction.
func (l *Ledger) RemoveCategory(ctx context.Context, ref string) error {
	c, err := l.CategoryAny(ctx, ref)
	if err != nil {
		return err
	}
	if c.System {
		return fmt.Errorf("%s is a system category and cannot be removed", c.Name)
	}
	held, err := l.categoryHolds(ctx, c)
	if err != nil {
		return err
	}
	if held != "" {
		return fmt.Errorf("%s %s: archive it instead, which keeps the history and takes it out of the pickers", c.Name, held)
	}
	_, err = l.db.ExecContext(ctx, `DELETE FROM categories WHERE id=?`, c.ID)
	return err
}

// categoryHolds reports in words what keeps a category alive, or "" when
// nothing does.
func (l *Ledger) categoryHolds(ctx context.Context, c Category) (string, error) {
	counts := []struct {
		query string
		one   string
		many  string
	}{
		{`SELECT COUNT(*) FROM categories WHERE parent_id=?`, "has a category under it", "has %d categories under it"},
		{`SELECT COUNT(*) FROM transactions WHERE category_id=?`, "has a transaction", "has %d transactions"},
		{`SELECT COUNT(*) FROM splits WHERE category_id=?`, "has a split line", "has %d split lines"},
		{`SELECT COUNT(*) FROM budgets WHERE category_id=?`, "has a budget line", "has %d budget lines"},
	}
	for _, q := range counts {
		var n int
		if err := l.db.QueryRowContext(ctx, q.query, c.ID).Scan(&n); err != nil {
			return "", err
		}
		switch {
		case n == 1:
			return q.one, nil
		case n > 1:
			return fmt.Sprintf(q.many, n), nil
		}
	}

	// Rules keep their transaction as JSON, so the only way to know is to
	// read them. There are never many.
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
		if t.CategoryID == c.ID {
			return fmt.Sprintf("is what %s posts to", name), nil
		}
	}
	return "", rows.Err()
}

func (l *Ledger) categoryIDTaken(ctx context.Context, id string) (bool, error) {
	var one int
	err := l.db.QueryRowContext(ctx, `SELECT 1 FROM categories WHERE id=?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func pathOfCategory(c Category) string {
	if c.ParentName != "" {
		return c.ParentName + " / " + c.Name
	}
	return c.Name
}
