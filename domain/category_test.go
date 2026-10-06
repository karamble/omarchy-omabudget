package domain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/karamble/omarchy-omabudget/db"
)

func TestAddCategory(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()

	group, err := l.AddCategory(ctx, Category{Name: "Side projects", Kind: "income", Icon: "󰄔"})
	if err != nil {
		t.Fatal(err)
	}
	if group.ID != "side-projects" || group.Kind != "income" || group.Behaviour != BehaviourMonthly || group.System || group.Archived {
		t.Fatalf("%+v", group)
	}

	child, err := l.AddCategory(ctx, Category{Name: "Plugin sales", ParentID: "Side projects"})
	if err != nil {
		t.Fatal(err)
	}
	if child.ID != "side-projects/plugin-sales" {
		t.Fatalf("id %q", child.ID)
	}
	if child.Kind != "income" {
		t.Fatalf("a child takes its parent's kind, got %q", child.Kind)
	}
	if child.ParentName != "Side projects" {
		t.Fatalf("%+v", child)
	}

	// A group keeps its own order, and a second child sorts after the first.
	second, err := l.AddCategory(ctx, Category{Name: "Consulting", ParentID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	if second.SortOrder <= child.SortOrder {
		t.Fatalf("sort %d then %d", child.SortOrder, second.SortOrder)
	}

	// The new categories are usable straight away.
	a := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	tx, err := l.Add(ctx, Transaction{Kind: Income, AccountID: a.ID, Amount: 25000, CategoryID: "Plugin sales"})
	if err != nil {
		t.Fatal(err)
	}
	if tx.CategoryID != "side-projects/plugin-sales" {
		t.Fatalf("category %q", tx.CategoryID)
	}
}

func TestAddCategoryRefusals(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	groceries := mustCategory(t, l, "Groceries")

	cases := []struct {
		name string
		in   Category
	}{
		{"no name", Category{Name: "  "}},
		{"a name with no letters", Category{Name: "!!!"}},
		{"a duplicate group", Category{Name: "Housing"}},
		{"a duplicate child", Category{Name: "Groceries", ParentID: "Food"}},
		{"a third level", Category{Name: "Organic", ParentID: groceries.ID}},
		{"a system parent", Category{Name: "Anything", ParentID: "sys-uncategorised"}},
		{"an unknown parent", Category{Name: "Anything", ParentID: "nope"}},
		{"an unknown kind", Category{Name: "Anything", Kind: "savings"}},
		{"an unknown behaviour", Category{Name: "Anything", Behaviour: "quarterly"}},
		{"a goal without a target", Category{Name: "Anything", Behaviour: BehaviourGoal}},
	}
	for _, c := range cases {
		if _, err := l.AddCategory(ctx, c.in); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
	}

	// The kind is the parent's, whatever the caller asked for.
	child, err := l.AddCategory(ctx, Category{Name: "Bakery", ParentID: "Food", Kind: "income"})
	if err != nil {
		t.Fatal(err)
	}
	if child.Kind != "expense" {
		t.Fatalf("kind %q", child.Kind)
	}
}

func TestAddCategorySlugCollision(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	first, err := l.AddCategory(ctx, Category{Name: "Coffee & snacks", ParentID: "Personal"})
	if err != nil {
		t.Fatal(err)
	}
	// The same slug already exists under Food, but under Personal it is free.
	if first.ID != "personal/coffee-snacks" {
		t.Fatalf("id %q", first.ID)
	}
	// Punctuation is all that separates these two names, so the id is numbered.
	second, err := l.AddCategory(ctx, Category{Name: "Coffee + snacks", ParentID: "Personal"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "personal/coffee-snacks-2" {
		t.Fatalf("id %q", second.ID)
	}
}

func TestUpdateCategory(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	coffee := mustCategory(t, l, "Coffee & snacks")
	tx, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 450, CategoryID: coffee.ID})
	if err != nil {
		t.Fatal(err)
	}

	edited := coffee
	edited.Name = "Coffee and cake"
	edited.Icon = "󰅐"
	edited.SortOrder = 3
	out, err := l.UpdateCategory(ctx, edited)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != coffee.ID {
		t.Fatalf("a rename moved the id: %q", out.ID)
	}
	if out.Name != "Coffee and cake" || out.Icon != "󰅐" || out.SortOrder != 3 {
		t.Fatalf("%+v", out)
	}
	// The transaction still points at it, under the new name.
	got, err := l.Get(ctx, tx.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CategoryID != coffee.ID {
		t.Fatalf("category %q", got.CategoryID)
	}
	index, err := l.CategoryIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if index.Name(got.CategoryID) != "Coffee and cake" {
		t.Fatalf("name %q", index.Name(got.CategoryID))
	}
	// And the old name no longer resolves.
	if _, err := l.Category(ctx, "Coffee & snacks"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("the old name still resolves: %v", err)
	}

	// A name another sibling already has is refused.
	clash := out
	clash.Name = "Groceries"
	if _, err := l.UpdateCategory(ctx, clash); err == nil {
		t.Fatal("a duplicate sibling name was accepted")
	}
	// A system category is not editable at all.
	sys, err := l.CategoryAny(ctx, "sys-uncategorised")
	if err != nil {
		t.Fatal(err)
	}
	sys.Name = "Misc"
	if _, err := l.UpdateCategory(ctx, sys); err == nil {
		t.Fatal("a system category was edited")
	}
	if _, err := l.ArchiveCategory(ctx, "sys-uncategorised", true); err == nil {
		t.Fatal("a system category was archived")
	}
}

func TestCategoryFlagsCascade(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	food, err := l.CategoryAny(ctx, "food")
	if err != nil {
		t.Fatal(err)
	}

	// Archiving a group must take its children out of the pickers too, since
	// transactions are booked on children.
	if _, err := l.ArchiveCategory(ctx, food.ID, true); err != nil {
		t.Fatal(err)
	}
	child, err := l.CategoryAny(ctx, "food/groceries")
	if err != nil {
		t.Fatal(err)
	}
	if !child.Archived {
		t.Fatal("the child stayed live under an archived group")
	}
	if _, err := l.Category(ctx, "Groceries"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("an archived category is still offered: %v", err)
	}
	if _, err := l.CategoryAny(ctx, "Groceries"); err != nil {
		t.Fatalf("an archived category is unreachable for management: %v", err)
	}
	// It is still refused for new entries.
	a := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 450, CategoryID: "Groceries"}); err == nil {
		t.Fatal("an archived category took a transaction")
	}

	if _, err := l.ArchiveCategory(ctx, food.ID, false); err != nil {
		t.Fatal(err)
	}
	if child, _ = l.CategoryAny(ctx, "food/groceries"); child.Archived {
		t.Fatal("the child stayed archived after the group came back")
	}

	// The statistics flag cascades the same way.
	food, _ = l.CategoryAny(ctx, "food")
	food.Excluded = true
	if _, err := l.UpdateCategory(ctx, food); err != nil {
		t.Fatal(err)
	}
	if child, _ = l.CategoryAny(ctx, "food/groceries"); !child.Excluded {
		t.Fatal("the child is still counted under an excluded group")
	}
	// A child added to an excluded group starts excluded.
	fresh, err := l.AddCategory(ctx, Category{Name: "Farm box", ParentID: "food"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.Excluded {
		t.Fatal("a new child of an excluded group is counted")
	}
}

func TestRemoveCategory(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 500000)

	// Nothing points at a category that was just made, so it goes.
	spare, err := l.AddCategory(ctx, Category{Name: "Spare", ParentID: "Personal"})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveCategory(ctx, spare.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.CategoryAny(ctx, spare.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	// And the id is free again, without a number.
	again, err := l.AddCategory(ctx, Category{Name: "Spare", ParentID: "Personal"})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != spare.ID {
		t.Fatalf("id %q, want %q", again.ID, spare.ID)
	}
	if err := l.RemoveCategory(ctx, again.ID); err != nil {
		t.Fatal(err)
	}

	held := func(t *testing.T, ref, want string) {
		t.Helper()
		err := l.RemoveCategory(ctx, ref)
		if err == nil {
			t.Fatalf("%s was removed while %s", ref, want)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want %q in it", ref, err, want)
		}
		if !strings.Contains(err.Error(), "archive it instead") {
			t.Fatalf("%s: %v, want the archive advice", ref, err)
		}
	}

	// A transaction holds one.
	spend(t, l, a, "Coffee & snacks", "2026-09-02", 450)
	held(t, "Coffee & snacks", "has a transaction")

	// A split line holds one.
	groceries := mustCategory(t, l, "Groceries")
	fuel := mustCategory(t, l, "Fuel")
	if _, err := l.Add(ctx, Transaction{Kind: Expense, AccountID: a.ID, Amount: 5000,
		Splits: []Split{{CategoryID: groceries.ID, Amount: 3000}, {CategoryID: fuel.ID, Amount: 2000}}}); err != nil {
		t.Fatal(err)
	}
	held(t, fuel.ID, "has a split line")

	// A budget line holds one.
	if err := l.SetBudget(ctx, "Clothing & footwear", "2026-09", 10000, ""); err != nil {
		t.Fatal(err)
	}
	held(t, "Clothing & footwear", "has a budget line")

	// A rule holds one.
	rent(t, l, a, "2026-09-03")
	held(t, "Rent", "posts to")

	// Children hold their group.
	held(t, "Food", "categories under it")

	// System categories never go.
	if err := l.RemoveCategory(ctx, "sys-uncategorised"); err == nil {
		t.Fatal("a system category was removed")
	}
	if err := l.RemoveCategory(ctx, "nope"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}

	// Archiving is what is left, and it keeps the history.
	if _, err := l.ArchiveCategory(ctx, "Coffee & snacks", true); err != nil {
		t.Fatal(err)
	}
	list, err := l.Transactions(ctx, Filter{CategoryID: "food/coffee-snacks"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("archiving lost the history: %d rows", len(list))
	}
}

// TestTranslateCategories walks a translation pass: a dry run writes nothing, a
// seeded row moves, a row the user renamed is left alone, running it twice has
// nothing left to do, and the way back to English is the same operation.
func TestTranslateCategories(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()

	// A stand-in for the language files: the names each id is known by, and
	// what this language calls it.
	known := map[string][]string{
		"housing":           {"Housing", "Wohnen"},
		"personal/hobbies":  {"Hobbies", "hobbies"},
		"housing/rent":      {"Rent", "Miete"},
		"food/groceries":    {"Groceries", "Lebensmittel"},
		"sys-uncategorised": {"Uncategorised", "Nicht kategorisiert"},
	}
	de := map[string]string{
		"housing": "Wohnen", "housing/rent": "Miete",
		"food/groceries": "Lebensmittel", "sys-uncategorised": "Nicht kategorisiert",
		// Differs from the seeded name only in case, which still has to move:
		// a word a language capitalises differently is a different word on
		// screen.
		"personal/hobbies": "hobbies",
	}
	en := map[string]string{
		"housing": "Housing", "housing/rent": "Rent",
		"food/groceries": "Groceries", "sys-uncategorised": "Uncategorised",
		"personal/hobbies": "Hobbies",
	}

	nameOf := func(id string) string {
		t.Helper()
		c, err := l.CategoryAny(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return c.Name
	}

	// The user renames one of them first. It is theirs from here on.
	groceries, err := l.CategoryAny(ctx, "food/groceries")
	if err != nil {
		t.Fatal(err)
	}
	groceries.Name = "Food shopping"
	if _, err := l.UpdateCategory(ctx, groceries); err != nil {
		t.Fatal(err)
	}

	// A dry run reports and writes nothing.
	out, err := l.TranslateCategories(ctx, "de", de, known, true)
	if err != nil {
		t.Fatal(err)
	}
	if !out.DryRun || len(out.Renamed) != 4 || len(out.Kept) != 1 || len(out.Missing) != 0 {
		t.Fatalf("%+v", out)
	}
	if out.Kept[0].ID != "food/groceries" || out.Kept[0].Name != "Food shopping" {
		t.Fatalf("kept %+v", out.Kept)
	}
	if got := nameOf("housing/rent"); got != "Rent" {
		t.Fatalf("a dry run wrote %q", got)
	}

	// The pass itself, system row included.
	out, err = l.TranslateCategories(ctx, "de", de, known, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Renamed) != 4 {
		t.Fatalf("%+v", out.Renamed)
	}
	for id, want := range map[string]string{
		"housing": "Wohnen", "housing/rent": "Miete", "personal/hobbies": "hobbies",
		"sys-uncategorised": "Nicht kategorisiert", "food/groceries": "Food shopping",
	} {
		if got := nameOf(id); got != want {
			t.Errorf("%s is %q, want %q", id, got, want)
		}
	}

	// Transactions are booked on ids, so the tree moved and history did not.
	if _, err := l.CategoryAny(ctx, "housing/rent"); err != nil {
		t.Fatalf("the id moved: %v", err)
	}

	// Run again and there is nothing left to do, but the user's own name is
	// still reported as kept rather than quietly forgotten.
	out, err = l.TranslateCategories(ctx, "de", de, known, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Renamed) != 0 || len(out.Kept) != 1 {
		t.Fatalf("a second pass did %+v", out)
	}

	// Back to English, by the same comparison.
	out, err = l.TranslateCategories(ctx, "en", en, known, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Renamed) != 4 {
		t.Fatalf("%+v", out.Renamed)
	}
	if got := nameOf("personal/hobbies"); got != "Hobbies" {
		t.Fatalf("a case-only difference did not come back: %q", got)
	}
	if got := nameOf("housing/rent"); got != "Rent" {
		t.Fatalf("back in English it is %q", got)
	}
	if got := nameOf("food/groceries"); got != "Food shopping" {
		t.Fatalf("the user's name did not survive the round trip: %q", got)
	}
}

// TestTranslateCategoriesRefusesCollision holds the invariant UpdateCategory
// enforces and the bulk write bypasses: two categories under one parent must
// not share a name. A language that would cause one is refused whole.
func TestTranslateCategoriesRefusesCollision(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()

	known := map[string][]string{"housing/rent": {"Rent"}}
	clash := map[string]string{"housing/rent": "Renovation"}

	if _, err := l.TranslateCategories(ctx, "xx", clash, known, false); err == nil {
		t.Fatal("a colliding name was accepted")
	} else if !strings.Contains(err.Error(), "housing/renovation") {
		t.Fatalf("the error does not name the other category: %v", err)
	}
	c, err := l.CategoryAny(ctx, "housing/rent")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Rent" {
		t.Fatalf("the refused pass still wrote %q", c.Name)
	}
}

// TestTranslateCategoriesReportsMissing checks that a category the user deleted
// is reported rather than planted again.
func TestTranslateCategoriesReportsMissing(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()

	target := map[string]string{"housing/moat": "Burggraben"}
	out, err := l.TranslateCategories(ctx, "de", target, map[string][]string{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Missing) != 1 || out.Missing[0] != "housing/moat" {
		t.Fatalf("%+v", out)
	}
	if _, err := l.CategoryAny(ctx, "housing/moat"); err == nil {
		t.Fatal("a missing category was planted")
	}
}
