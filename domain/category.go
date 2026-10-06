package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// Translation reports what a translation pass did, or what a dry run would do.
type Translation struct {
	Language string    `json:"language"`
	DryRun   bool      `json:"dryRun"`
	Renamed  []Renamed `json:"renamed"`
	Kept     []Kept    `json:"kept"`
	Missing  []string  `json:"missing"`
}

// Renamed is one category this pass moved to another language.
type Renamed struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Kept is a seeded category carrying a name the user gave it, which a
// translation pass leaves exactly as it found it.
type Kept struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// namedIn reports whether name is one of the names list carries, compared the
// way the rest of this file compares category names.
func namedIn(list []string, name string) bool {
	name = strings.TrimSpace(name)
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), name) {
			return true
		}
	}
	return false
}

// TranslateCategories renames the seeded categories into another language.
//
// target is the name each seeded id takes, keyed by id, and known is every name
// an id carries in any shipped language. A row is renamed only while it still
// carries one of its known names: a category the user renamed is theirs and is
// reported as kept, a pass run twice changes nothing the second time, and the
// way back to English is the same comparison rather than a special case.
//
// A seeded id with no row is reported as missing rather than planted again,
// because a category the user deleted should stay deleted.
//
// This writes the system rows too, which UpdateCategory refuses. That refusal
// is there to stop a user editing rows the ledger books against; the names are
// labels like any other, and leaving them out is how a tree ends up half
// translated.
func (l *Ledger) TranslateCategories(ctx context.Context, language string,
	target map[string]string, known map[string][]string, dry bool) (Translation, error) {

	out := Translation{
		Language: language,
		DryRun:   dry,
		Renamed:  []Renamed{},
		Kept:     []Kept{},
		Missing:  []string{},
	}

	// Read every live row and drain the cursor before anything else runs. The
	// pool is pinned to one connection, so a statement issued while these rows
	// are open waits for a connection that this loop is holding.
	type row struct{ name, parent string }
	rows, err := l.db.QueryContext(ctx,
		`SELECT id,name,COALESCE(parent_id,'') FROM categories WHERE deleted_at IS NULL`)
	if err != nil {
		return Translation{}, err
	}
	current := map[string]row{}
	for rows.Next() {
		var id string
		var r row
		if err := rows.Scan(&id, &r.name, &r.parent); err != nil {
			rows.Close()
			return Translation{}, err
		}
		current[id] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Translation{}, err
	}

	ids := make([]string, 0, len(target))
	for id := range target {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		want := strings.TrimSpace(target[id])
		if want == "" {
			return Translation{}, fmt.Errorf("%s: this language gives it no name", id)
		}
		cur, ok := current[id]
		switch {
		case !ok:
			out.Missing = append(out.Missing, id)
		case !namedIn(known[id], cur.name):
			out.Kept = append(out.Kept, Kept{ID: id, Name: cur.name})
		case strings.TrimSpace(cur.name) == want:
			// Already in this language. Neither renamed nor kept back, so a
			// second pass reports nothing to do.
			//
			// Compared exactly, unlike the match above. A name differing only
			// in case is a different name on screen, and German capitalises
			// nouns the English spelling of the same word leaves lowercase.
		default:
			out.Renamed = append(out.Renamed, Renamed{ID: id, From: cur.name, To: want})
		}
	}

	// The writes below go straight to SQL and so miss the sibling-name check
	// UpdateCategory makes. Two categories under one parent sharing a name is
	// what the rest of the app reads as ambiguous, so settle the whole result
	// first and refuse all of it rather than commit half. User categories are
	// in this check too: a translated name can collide with one of theirs.
	final := make(map[string]string, len(current))
	for id, r := range current {
		final[id] = r.name
	}
	for _, r := range out.Renamed {
		final[r.ID] = r.To
	}
	allIDs := make([]string, 0, len(final))
	for id := range final {
		allIDs = append(allIDs, id)
	}
	sort.Strings(allIDs)
	seen := map[string]string{}
	for _, id := range allIDs {
		key := current[id].parent + "\x00" + strings.ToLower(strings.TrimSpace(final[id]))
		if other, dup := seen[key]; dup {
			return Translation{}, fmt.Errorf("%s in %s would be named %q, which %s already carries under the same parent",
				id, language, final[id], other)
		}
		seen[key] = id
	}

	if dry || len(out.Renamed) == 0 {
		return out, nil
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Translation{}, err
	}
	defer tx.Rollback()
	upd, err := tx.PrepareContext(ctx, `UPDATE categories SET name=? WHERE id=?`)
	if err != nil {
		return Translation{}, err
	}
	defer upd.Close()
	for _, r := range out.Renamed {
		if _, err := upd.ExecContext(ctx, r.To, r.ID); err != nil {
			return Translation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Translation{}, err
	}
	return out, nil
}
