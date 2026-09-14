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
