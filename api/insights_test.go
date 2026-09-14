package api

import (
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
	tight.Bills = []billRow{{Due: domain.Due{Name: "Rent", Kind: domain.Expense, Amount: 120000, DaysUntil: 3}}}
	got = insights(tight)
	if len(got) == 0 || got[0].Kind != "bills" || got[0].Tone != toneBad {
		t.Fatalf("%+v", got)
	}

	// An overdue bill is said plainly, and outranks the shortfall.
	late := tight
	late.Bills = []billRow{{Due: domain.Due{Name: "Rent", Kind: domain.Expense, Amount: 120000, Overdue: true}}}
	got = insights(late)
	if got[0].Text != "1 bill is overdue" {
		t.Fatalf("%q", got[0].Text)
	}

	// Never more than three.
	full := over
	full.Liquid = 1000
	full.Bills = []billRow{{Due: domain.Due{Name: "Rent", Kind: domain.Expense, Amount: 120000, Overdue: true}}}
	if got := insights(full); len(got) != 3 {
		t.Fatalf("%d insights", len(got))
	}
}
