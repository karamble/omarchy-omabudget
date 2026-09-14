package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/karamble/omarchy-omabudget/domain"
)

func TestBudgetHelpersAPI(t *testing.T) {
	s, l := newTestServer(t, 1)
	a := mustAccount(t, l, "Main", domain.Checking, 1000000)
	groceries, _ := l.Category(t.Context(), "Groceries")
	for _, d := range []string{"2026-06-05", "2026-07-05", "2026-08-05"} {
		mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: a.ID, Amount: 30000, CategoryID: groceries.ID, Date: d})
	}
	call(t, s, "PUT", "/api/budgets", map[string]any{"category": "Groceries", "period": "2026-09", "planned": "250"})

	var out struct {
		Changed int `json:"changed"`
		domain.Budget
	}
	code, body := call(t, s, "POST", "/api/budgets/helpers", map[string]any{"action": "average", "period": "2026-09", "periods": 3})
	if code != http.StatusOK {
		t.Fatalf("average: %d %s", code, body)
	}
	json.Unmarshal(body, &out)
	if out.Changed != 1 || len(out.Cards) != 1 || out.Cards[0].Planned != 30000 {
		t.Fatalf("%+v", out)
	}
	code, body = call(t, s, "POST", "/api/budgets/helpers", map[string]any{"action": "copy", "period": "2026-10"})
	if code != http.StatusOK {
		t.Fatalf("copy: %d %s", code, body)
	}
	json.Unmarshal(body, &out)
	if out.Changed != 1 || out.Period != "2026-10" || out.Cards[0].Planned != 30000 {
		t.Fatalf("%+v", out)
	}
	code, body = call(t, s, "POST", "/api/budgets/helpers", map[string]any{"action": "scale", "period": "2026-10", "percent": 10})
	if code != http.StatusOK {
		t.Fatalf("scale: %d %s", code, body)
	}
	json.Unmarshal(body, &out)
	if out.Cards[0].Planned != 33000 {
		t.Fatalf("%+v", out)
	}
	code, _ = call(t, s, "POST", "/api/budgets/helpers", map[string]any{"action": "halve"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown action: %d", code)
	}

	// The alerts snapshot reads the same figures.
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: a.ID, Amount: 29000, CategoryID: groceries.ID, Date: "2026-09-10"})
	snap := s.Snapshot()
	if snap.Numbers["budget.categoriesAtWarn"] != 1 || snap.Numbers["budget.categoriesOver"] != 0 || len(snap.Lists["budget.warn"]) != 1 {
		t.Fatalf("%+v", snap.Numbers)
	}
	if snap.Numbers["period.spent"] != 29000 {
		t.Fatalf("period.spent %v", snap.Numbers["period.spent"])
	}
}
