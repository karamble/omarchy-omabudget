package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/karamble/omarchy-omabudget/domain"
)

// TestEditTransaction sends the form back with changes and checks the row,
// the listing filters and the deleted listing.
func TestEditTransaction(t *testing.T) {
	s, l := newTestServer(t, 1)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", domain.Checking, 100000)
	spare := mustAccount(t, l, "Spare", domain.Savings, 0)

	code, body := call(t, s, "POST", "/api/transactions", map[string]any{
		"amount": "4.50", "category": "Coffee & snacks", "description": "Coffee at the station", "date": "2026-09-02"})
	if code != http.StatusCreated {
		t.Fatalf("add: %d %s", code, body)
	}
	var added domain.Transaction
	json.Unmarshal(body, &added)

	code, body = call(t, s, "GET", "/api/transactions/"+added.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("get: %d %s", code, body)
	}

	code, body = call(t, s, "PUT", "/api/transactions/"+added.ID, map[string]any{
		"kind": "expense", "account": "Main", "amount": "12.80", "category": "Work lunches",
		"date": "2026-09-03", "description": "Lunch", "tags": []string{"work"}, "status": "reconciled"})
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}
	var edited domain.Transaction
	json.Unmarshal(body, &edited)
	if edited.ID != added.ID || edited.Amount != -1280 || edited.Description != "Lunch" || edited.Status != domain.Reconciled || len(edited.Tags) != 1 {
		t.Fatalf("%+v", edited)
	}
	if b, _ := l.Balance(ctx, main.ID); b.Minor != 100000-1280 {
		t.Fatalf("balance %d", b.Minor)
	}

	// The same form turns it into a transfer.
	code, body = call(t, s, "PUT", "/api/transactions/"+added.ID, map[string]any{
		"kind": "transfer", "account": "Main", "counterAccount": "Spare", "amount": "50", "date": "2026-09-03"})
	if code != http.StatusOK {
		t.Fatalf("put transfer: %d %s", code, body)
	}
	if b, _ := l.Balance(ctx, spare.ID); b.Minor != 5000 {
		t.Fatalf("spare %d", b.Minor)
	}

	code, body = call(t, s, "PUT", "/api/transactions/nope", map[string]any{"amount": "1"})
	if code != http.StatusNotFound {
		t.Fatalf("unknown id: %d %s", code, body)
	}

	// Listing filters.
	call(t, s, "POST", "/api/transactions", map[string]any{"amount": "3250", "category": "Primary salary", "kind": "income", "description": "Salary", "date": "2026-09-01"})
	call(t, s, "POST", "/api/transactions", map[string]any{"amount": "60", "category": "Groceries", "description": "Weekly shop", "notes": "with the kids", "date": "2026-09-05"})
	count := func(query string) int {
		t.Helper()
		code, body := call(t, s, "GET", "/api/transactions?"+query, nil)
		if code != http.StatusOK {
			t.Fatalf("list %s: %d %s", query, code, body)
		}
		var list []domain.Transaction
		json.Unmarshal(body, &list)
		return len(list)
	}
	if n := count(""); n != 3 {
		t.Fatalf("all: %d", n)
	}
	if n := count("q=kids"); n != 1 {
		t.Fatalf("search: %d", n)
	}
	if n := count("kind=income"); n != 1 {
		t.Fatalf("kind: %d", n)
	}
	if n := count("limit=1&offset=2"); n != 1 {
		t.Fatalf("offset: %d", n)
	}
	call(t, s, "DELETE", "/api/transactions/"+added.ID, nil)
	if n := count(""); n != 2 {
		t.Fatalf("after delete: %d", n)
	}
	if n := count("deleted=1"); n != 1 {
		t.Fatalf("deleted: %d", n)
	}
}

func TestEditAccount(t *testing.T) {
	s, l := newTestServer(t, 1)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", domain.Checking, 100000)

	code, body := call(t, s, "PUT", "/api/accounts/"+a.ID, map[string]any{
		"name": "Everyday", "institution": "House bank", "lowBalance": "500", "includeInNetWorth": false})
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}
	var row accountRow
	json.Unmarshal(body, &row)
	if row.Name != "Everyday" || row.Institution != "House bank" || row.LowBalance == nil || *row.LowBalance != 50000 || row.IncludeInNetWorth || row.Balance != 100000 {
		t.Fatalf("%+v", row)
	}

	code, body = call(t, s, "PUT", "/api/accounts/"+a.ID, map[string]any{"lowBalance": "", "opening": "0"})
	if code != http.StatusOK {
		t.Fatalf("clear: %d %s", code, body)
	}
	got, _ := l.Account(ctx, a.ID)
	if got.LowBalance != nil || got.OpeningBalance != 0 {
		t.Fatalf("%+v", got)
	}

	code, body = call(t, s, "PUT", "/api/accounts/"+a.ID, map[string]any{"name": " "})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("empty name: %d %s", code, body)
	}
	code, body = call(t, s, "PUT", "/api/accounts/nope", map[string]any{"name": "x"})
	if code != http.StatusNotFound {
		t.Fatalf("unknown: %d %s", code, body)
	}
}
