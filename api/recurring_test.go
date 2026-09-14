package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/karamble/omarchy-omabudget/domain"
)

// TestRulesAPI walks a rule through the API: add, list, upcoming, post,
// skip, edit, remove, and the dashboard's bills.
func TestRulesAPI(t *testing.T) {
	s, l := newTestServer(t, 1)
	mustAccount(t, l, "Main", domain.Checking, 500000)

	code, body := call(t, s, "POST", "/api/rules", map[string]any{
		"name": "Rent", "amount": "1200", "category": "Rent", "frequency": "monthly", "startDate": "2026-09-03"})
	if code != http.StatusCreated {
		t.Fatalf("add: %d %s", code, body)
	}
	var rule domain.Rule
	json.Unmarshal(body, &rule)
	if rule.Template.Amount != 120000 || rule.Template.CategoryID != "housing/rent" || rule.LeadDays != 3 || rule.NextDue != "2026-09-03" {
		t.Fatalf("%+v", rule)
	}
	code, body = call(t, s, "POST", "/api/rules", map[string]any{"name": "Broken", "amount": "10", "category": "Rent", "frequency": "yearly", "startDate": "2026-09-03"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("bad frequency: %d %s", code, body)
	}

	code, body = call(t, s, "GET", "/api/bills?days=30", nil)
	if code != http.StatusOK {
		t.Fatalf("bills: %d %s", code, body)
	}
	var bills []billRow
	json.Unmarshal(body, &bills)
	if len(bills) != 2 || !bills[0].Overdue || bills[0].CategoryName != "Rent" || bills[0].AccountName != "Main" || bills[1].Date != "2026-10-03" {
		t.Fatalf("%+v", bills)
	}

	code, body = call(t, s, "POST", "/api/rules/"+rule.ID+"/post", map[string]any{"amount": "1250"})
	if code != http.StatusCreated {
		t.Fatalf("post: %d %s", code, body)
	}
	var posted domain.Transaction
	json.Unmarshal(body, &posted)
	if posted.Amount != -125000 || posted.Date != "2026-09-03" || posted.RecurringRuleID != rule.ID {
		t.Fatalf("%+v", posted)
	}

	code, body = call(t, s, "POST", "/api/rules/"+rule.ID+"/skip", nil)
	if code != http.StatusOK {
		t.Fatalf("skip: %d %s", code, body)
	}
	json.Unmarshal(body, &rule)
	if rule.NextDue != "2026-11-03" || rule.Posted != 1 {
		t.Fatalf("%+v", rule)
	}

	code, body = call(t, s, "PUT", "/api/rules/"+rule.ID, map[string]any{
		"name": "Rent", "amount": "1300", "category": "Rent", "frequency": "monthly", "startDate": "2026-09-03", "autoPost": true})
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}
	json.Unmarshal(body, &rule)
	if rule.Template.Amount != 130000 || !rule.AutoPost || rule.NextDue != "2026-11-03" {
		t.Fatalf("%+v", rule)
	}

	code, body = call(t, s, "GET", "/api/dashboard", nil)
	if code != http.StatusOK {
		t.Fatalf("dashboard: %d", code)
	}
	var doc struct {
		Bills []billRow `json:"bills"`
	}
	json.Unmarshal(body, &doc)
	if len(doc.Bills) != 0 {
		t.Fatalf("november is outside thirty days: %+v", doc.Bills)
	}

	code, body = call(t, s, "GET", "/api/rules", nil)
	var rules []domain.Rule
	json.Unmarshal(body, &rules)
	if code != http.StatusOK || len(rules) != 1 {
		t.Fatalf("list: %d %s", code, body)
	}
	code, _ = call(t, s, "DELETE", "/api/rules/"+rule.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("remove: %d", code)
	}
	code, _ = call(t, s, "DELETE", "/api/rules/"+rule.ID, nil)
	if code != http.StatusNotFound {
		t.Fatalf("second remove: %d", code)
	}
}
