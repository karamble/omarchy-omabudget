package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/karamble/omarchy-omabudget/domain"
)

func TestInsights(t *testing.T) {
	base := dashboardOut{
		BaseCurrency: "EUR",
		Period:       period{Days: 30, Elapsed: 15},
		Totals:       domain.Totals{Income: 300000, SavingsRate: 40},
		Previous:     domain.Totals{Income: 300000, SavingsRate: 20},
		Liquid:       100000,
	}

	// Nothing planned and nothing due: nothing worth saying.
	if got := insights(base); len(got) != 1 || got[0].Kind != "savings" {
		t.Fatalf("%+v", got)
	}

	// A category past its plan outranks everything else.
	over := base
	over.Budget = domain.Budget{Planned: 100000, Spent: 90000, Cards: []domain.BudgetCard{
		{Name: "Groceries", Planned: 30000, Spent: 45000},
		{Name: "Fuel", Planned: 20000, Spent: 22000},
	}}
	got := insights(over)
	if len(got) == 0 || got[0].Kind != "overspent" || got[0].Tone != toneBad {
		t.Fatalf("%+v", got)
	}
	if got[0].Text != "2 categories are past their plan, Groceries worst by 150.00 EUR" {
		t.Fatalf("%q", got[0].Text)
	}
	// Half the period gone and nine tenths of the plan spent: ahead of it.
	if len(got) < 2 || got[1].Kind != "pace" || got[1].Tone != toneWarn {
		t.Fatalf("%+v", got)
	}

	// A bill that costs more than there is to pay it with.
	tight := base
	tight.Liquid = 5000
	tight.Bills = []billRow{{Due: domain.Due{Name: "Rent", Kind: domain.Expense, Amount: 120000, DaysUntil: 3}, BaseAmount: 120000}}
	got = insights(tight)
	if len(got) == 0 || got[0].Kind != "bills" || got[0].Tone != toneBad {
		t.Fatalf("%+v", got)
	}

	// An overdue bill is said plainly, and outranks the shortfall.
	late := tight
	late.Bills = []billRow{{Due: domain.Due{Name: "Rent", Kind: domain.Expense, Amount: 120000, Overdue: true}, BaseAmount: 120000}}
	got = insights(late)
	if got[0].Text != "1 bill is overdue" {
		t.Fatalf("%q", got[0].Text)
	}

	// Never more than three.
	full := over
	full.Liquid = 1000
	full.Bills = []billRow{{Due: domain.Due{Name: "Rent", Kind: domain.Expense, Amount: 120000, Overdue: true}, BaseAmount: 120000}}
	if got := insights(full); len(got) != 3 {
		t.Fatalf("%d insights", len(got))
	}
}

// TestForeignBillsAreCompared: a bill in another currency is held against
// liquid funds at the rate on file and listed among what is due, in the base.
func TestForeignBillsAreCompared(t *testing.T) {
	s, l := newTestServer(t, 1)
	ctx := t.Context()
	if _, err := l.SetRate(ctx, "PLN", "0.25", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	mustAccount(t, l, "Main", domain.Checking, 20000)
	zloty, err := l.AddAccount(ctx, domain.Account{Name: "Zloty", Type: domain.Checking, Currency: "PLN", OpeningDate: "2026-01-01", IncludeInNetWorth: true})
	if err != nil {
		t.Fatal(err)
	}
	// 1,600 PLN due in three days is 400 EUR, more than the 200 on hand.
	if code, body := call(t, s, "POST", "/api/rules", map[string]any{
		"name": "Rent", "account": zloty.ID, "amount": "1600", "category": "Rent", "frequency": "monthly", "startDate": "2026-09-16"}); code != http.StatusCreated {
		t.Fatalf("add: %d %s", code, body)
	}
	code, body := call(t, s, "GET", "/api/dashboard", nil)
	if code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", code, body)
	}
	var doc dashboardOut
	json.Unmarshal(body, &doc)
	if len(doc.Bills) != 1 || doc.Bills[0].Amount != 160000 || doc.Bills[0].BaseAmount != 40000 {
		t.Fatalf("bills: %+v", doc.Bills)
	}
	var bills []Insight
	for _, in := range doc.Insights {
		if in.Kind == "bills" {
			bills = append(bills, in)
		}
	}
	if len(bills) != 1 || bills[0].Tone != toneBad || bills[0].Text != "400.00 EUR due in the next week, more than the 200.00 EUR on hand" {
		t.Fatalf("insights: %+v", doc.Insights)
	}

	// Shown in PLN, the same bill reads as typed and the figures follow.
	if code, body := call(t, s, "PUT", "/api/settings", map[string]any{"baseCurrency": "PLN"}); code != http.StatusOK {
		t.Fatalf("base: %d %s", code, body)
	}
	_, body = call(t, s, "GET", "/api/dashboard", nil)
	json.Unmarshal(body, &doc)
	if doc.Bills[0].BaseAmount != 160000 || doc.Liquid != 80000 {
		t.Fatalf("in PLN: bill %d liquid %d", doc.Bills[0].BaseAmount, doc.Liquid)
	}
}
