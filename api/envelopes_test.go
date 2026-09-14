package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/karamble/omarchy-omabudget/domain"
)

func TestEnvelopesAndSettingsAPI(t *testing.T) {
	s, l := newTestServer(t, 1)
	a := mustAccount(t, l, "Main", domain.Checking, 200000)
	fuel, _ := l.Category(t.Context(), "Fuel")

	code, body := call(t, s, "PUT", "/api/categories/"+fuel.ID, map[string]any{"behaviour": "rollover"})
	if code != http.StatusOK {
		t.Fatalf("behaviour: %d %s", code, body)
	}
	code, body = call(t, s, "PUT", "/api/categories/"+fuel.ID, map[string]any{"goalTarget": "600", "goalDue": "2026-12"})
	if code != http.StatusOK {
		t.Fatalf("goal: %d %s", code, body)
	}
	var c domain.Category
	json.Unmarshal(body, &c)
	if c.Behaviour != domain.BehaviourGoal || c.GoalTarget != 60000 || c.GoalDue != "2026-12" {
		t.Fatalf("%+v", c)
	}

	call(t, s, "PUT", "/api/budgets", map[string]any{"category": "Fuel", "period": "2026-08", "planned": "100"})
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: a.ID, Amount: 4000, CategoryID: fuel.ID, Date: "2026-08-10"})

	code, body = call(t, s, "POST", "/api/budgets/rollover", map[string]any{"from": "2026-08", "to": "2026-09"})
	if code != http.StatusOK {
		t.Fatalf("rollover: %d %s", code, body)
	}
	var rolled struct {
		Changed int `json:"changed"`
		domain.Envelopes
	}
	json.Unmarshal(body, &rolled)
	if rolled.Changed != 1 || len(rolled.Items) != 1 || rolled.Items[0].RolloverIn != 6000 {
		t.Fatalf("%+v", rolled)
	}

	code, body = call(t, s, "GET", "/api/envelopes?period=2026-09", nil)
	if code != http.StatusOK {
		t.Fatalf("envelopes: %d %s", code, body)
	}
	var e domain.Envelopes
	json.Unmarshal(body, &e)
	// 600 by December from 60 in the pot over four periods: 135 a period.
	if e.Liquid != 196000 || e.Held != 6000 || e.ToBeBudgeted != 190000 || e.Items[0].Accrual != 13500 || e.Items[0].MonthsLeft != 4 {
		t.Fatalf("%+v", e)
	}

	code, body = call(t, s, "PUT", "/api/settings", map[string]any{"model": "envelope", "periodStartDay": 10, "largeAmount": "500"})
	if code != http.StatusOK {
		t.Fatalf("settings: %d %s", code, body)
	}
	var st settingsOut
	json.Unmarshal(body, &st)
	if st.Model != "envelope" || st.PeriodStartDay != 10 || st.LargeAmount != 50000 || st.BaseCurrency != "EUR" {
		t.Fatalf("%+v", st)
	}
	code, _ = call(t, s, "PUT", "/api/settings", map[string]any{"model": "zero-based"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("bad model: %d", code)
	}
	code, _ = call(t, s, "PUT", "/api/settings", map[string]any{"periodStartDay": 31})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("bad start day: %d", code)
	}

	code, body = call(t, s, "GET", "/api/dashboard", nil)
	var doc struct {
		Model     string `json:"model"`
		Envelopes struct {
			ToBeBudgeted int64 `json:"toBeBudgeted"`
		} `json:"envelopes"`
	}
	json.Unmarshal(body, &doc)
	if code != http.StatusOK || doc.Model != "envelope" {
		t.Fatalf("dashboard model: %d %s", code, doc.Model)
	}
}
