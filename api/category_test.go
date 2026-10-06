package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/karamble/omarchy-omabudget/domain"
)

// TestCategoryAPI walks a category through the endpoints: create, rename,
// re-icon, take out of statistics, archive, restore, remove.
func TestCategoryAPI(t *testing.T) {
	s, l := newTestServer(t, 1)
	ctx := t.Context()

	code, body := call(t, s, "POST", "/api/categories", map[string]any{"name": "Side projects", "kind": "income", "icon": "󰄔"})
	if code != http.StatusCreated {
		t.Fatalf("add group: %d %s", code, body)
	}
	var group domain.Category
	json.Unmarshal(body, &group)
	if group.ID != "side-projects" || group.Kind != "income" {
		t.Fatalf("%+v", group)
	}

	code, body = call(t, s, "POST", "/api/categories", map[string]any{"name": "Plugin sales", "parent": "Side projects"})
	if code != http.StatusCreated {
		t.Fatalf("add child: %d %s", code, body)
	}
	var child domain.Category
	json.Unmarshal(body, &child)
	if child.ID != "side-projects/plugin-sales" || child.Kind != "income" {
		t.Fatalf("%+v", child)
	}

	code, body = call(t, s, "POST", "/api/categories", map[string]any{"name": "Housing"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate: %d %s", code, body)
	}

	// The id carries a slash, so the route has to take the rest of the path.
	code, body = call(t, s, "PUT", "/api/categories/"+child.ID, map[string]any{
		"name": "Plugin income", "icon": "󰄨", "excludedFromStatistics": true, "sortOrder": 4})
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}
	json.Unmarshal(body, &child)
	if child.Name != "Plugin income" || child.Icon != "󰄨" || !child.Excluded || child.SortOrder != 4 || child.ID != "side-projects/plugin-sales" {
		t.Fatalf("%+v", child)
	}

	// Behaviour and goal still work through the same endpoint.
	code, body = call(t, s, "PUT", "/api/categories/food/groceries", map[string]any{"behaviour": "rollover"})
	if code != http.StatusOK {
		t.Fatalf("behaviour: %d %s", code, body)
	}
	var groceries domain.Category
	json.Unmarshal(body, &groceries)
	if groceries.Behaviour != domain.BehaviourRollover {
		t.Fatalf("%+v", groceries)
	}

	// Archiving a group reaches its children, and the listing shows it.
	code, body = call(t, s, "PUT", "/api/categories/food", map[string]any{"archived": true})
	if code != http.StatusOK {
		t.Fatalf("archive: %d %s", code, body)
	}
	code, body = call(t, s, "GET", "/api/categories", nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	var list []domain.Category
	json.Unmarshal(body, &list)
	archived := 0
	for _, c := range list {
		if c.Archived {
			archived++
		}
	}
	if archived < 2 {
		t.Fatalf("the listing hides archived rows: %d of %d", archived, len(list))
	}
	code, _ = call(t, s, "PUT", "/api/categories/food", map[string]any{"archived": false})
	if code != http.StatusOK {
		t.Fatalf("restore: %d", code)
	}

	// A system category is refused, whatever is asked of it.
	code, body = call(t, s, "PUT", "/api/categories/sys-uncategorised", map[string]any{"name": "Misc"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("system rename: %d %s", code, body)
	}
	code, _ = call(t, s, "DELETE", "/api/categories/sys-uncategorised", nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("system remove: %d", code)
	}

	// Removal: refused while something points at it, then allowed.
	a := mustAccount(t, l, "Main", domain.Checking, 100000)
	mustAdd(t, l, domain.Transaction{Kind: domain.Income, AccountID: a.ID, Amount: 25000, CategoryID: child.ID, Date: "2026-09-02"})
	code, body = call(t, s, "DELETE", "/api/categories/"+child.ID, nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(string(body), "archive it instead") {
		t.Fatalf("held: %d %s", code, body)
	}
	code, body = call(t, s, "DELETE", "/api/categories/"+group.ID, nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(string(body), "under it") {
		t.Fatalf("group with a child: %d %s", code, body)
	}

	code, body = call(t, s, "POST", "/api/categories", map[string]any{"name": "Spare", "parent": "Personal"})
	if code != http.StatusCreated {
		t.Fatalf("add spare: %d %s", code, body)
	}
	var spare domain.Category
	json.Unmarshal(body, &spare)
	code, body = call(t, s, "DELETE", "/api/categories/"+spare.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("remove: %d %s", code, body)
	}
	if _, err := l.CategoryAny(ctx, spare.ID); err == nil {
		t.Fatal("still there")
	}
	code, _ = call(t, s, "DELETE", "/api/categories/nope", nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown: %d", code)
	}
}

// TestTranslateCategoriesAPI covers the wiring rather than the rules, which
// the domain tests hold: that the route is reachable, that the names come out
// of the embedded language files, and that a bad tag is refused before any of
// it runs.
func TestTranslateCategoriesAPI(t *testing.T) {
	s, l := newTestServer(t, 1)
	ctx := t.Context()

	// A tag that could name a file outside the directory never reaches one.
	code, body := call(t, s, "POST", "/api/categories/translate", map[string]any{"language": "../../etc/passwd"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("a path as a language: %d %s", code, body)
	}
	code, body = call(t, s, "POST", "/api/categories/translate", map[string]any{"language": "xx"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("an unshipped language: %d %s", code, body)
	}

	// English against a freshly seeded ledger has nothing to do, which is the
	// quietest proof that the embedded names and the seed agree.
	code, body = call(t, s, "POST", "/api/categories/translate", map[string]any{"language": "en", "dryRun": true})
	if code != http.StatusOK {
		t.Fatalf("en dry run: %d %s", code, body)
	}
	var out domain.Translation
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if !out.DryRun || out.Language != "en" {
		t.Fatalf("%+v", out)
	}
	if len(out.Renamed) != 0 || len(out.Kept) != 0 || len(out.Missing) != 0 {
		t.Fatalf("a fresh English ledger is not already English: %+v", out)
	}

	// Rename one by hand and English then reports it as the user's own.
	rent, err := l.CategoryAny(ctx, "housing/rent")
	if err != nil {
		t.Fatal(err)
	}
	rent.Name = "Flat"
	if _, err := l.UpdateCategory(ctx, rent); err != nil {
		t.Fatal(err)
	}
	code, body = call(t, s, "POST", "/api/categories/translate", map[string]any{"language": "en"})
	if code != http.StatusOK {
		t.Fatalf("en: %d %s", code, body)
	}
	out = domain.Translation{}
	json.Unmarshal(body, &out)
	if len(out.Kept) != 1 || out.Kept[0].ID != "housing/rent" || out.Kept[0].Name != "Flat" {
		t.Fatalf("%+v", out.Kept)
	}
}
