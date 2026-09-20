package api

import (
	"fmt"

	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/money"
)

// Insights are the two or three things worth saying about a period that the
// figures do not say on their own: whether spending is ahead of the plan, what
// has gone past it, what is about to be taken, and which way the savings rate
// moved. They are read-only and derived from the dashboard's own numbers, so
// the app, the command line and an agent all see the same sentences.

// Insight is one line, with a tone the app colours by.
type Insight struct {
	// Kind is stable: pace, overspent, bills, savings.
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Tone is good, warn, bad or plain.
	Tone string `json:"tone"`
}

const (
	toneGood  = "good"
	toneWarn  = "warn"
	toneBad   = "bad"
	tonePlain = "plain"
)

// insights ranks what is worth saying, worst first, and keeps the top three.
func insights(out dashboardOut) []Insight {
	cur := out.BaseCurrency
	amount := func(minor int64) string { return money.New(minor, cur).Format() + " " + cur }
	all := []Insight{}

	// ---- what has already gone past its plan
	over, worst := 0, ""
	var worstBy int64
	for _, c := range out.Budget.Cards {
		if c.Planned > 0 && c.Spent > c.Planned {
			over++
			if by := c.Spent - c.Planned; by > worstBy {
				worstBy, worst = by, c.Name
			}
		}
	}
	if over == 1 {
		all = append(all, Insight{Kind: "overspent", Tone: toneBad,
			Text: fmt.Sprintf("%s is %s past its plan", worst, amount(worstBy))})
	} else if over > 1 {
		all = append(all, Insight{Kind: "overspent", Tone: toneBad,
			Text: fmt.Sprintf("%d categories are past their plan, %s worst by %s", over, worst, amount(worstBy))})
	}

	// ---- pace against the plan, which only means something part way through
	if out.Budget.Planned > 0 && out.Period.Days > 0 && out.Period.Elapsed > 0 && out.Period.Elapsed < out.Period.Days {
		expected := out.Budget.Planned * int64(out.Period.Elapsed) / int64(out.Period.Days)
		left := out.Period.Days - out.Period.Elapsed
		switch {
		case expected > 0 && out.Budget.Spent > expected*11/10:
			all = append(all, Insight{Kind: "pace", Tone: toneWarn,
				Text: fmt.Sprintf("Spending is ahead of plan: %s of %s with %d days left",
					amount(out.Budget.Spent), amount(out.Budget.Planned), left)})
		case expected > 0 && out.Budget.Spent < expected*9/10:
			all = append(all, Insight{Kind: "pace", Tone: toneGood,
				Text: fmt.Sprintf("Spending is behind plan: %s of %s with %d days left",
					amount(out.Budget.Spent), amount(out.Budget.Planned), left)})
		}
	}

	// ---- what is about to be taken, against what there is to take it from
	var dueTotal int64
	dueCount, overdue := 0, 0
	for _, b := range out.Bills {
		// A rule carries its amount unsigned; the kind says which way it goes.
		// The sum is in the base, like the liquid figure it is held against,
		// so a bill whose currency has no rate cannot be counted.
		if b.Kind != domain.Expense || b.BaseAmount == 0 {
			continue
		}
		if b.Overdue {
			overdue++
		}
		if b.Overdue || b.DaysUntil <= 7 {
			dueCount++
			dueTotal += b.BaseAmount
		}
	}
	switch {
	case overdue > 0:
		all = append(all, Insight{Kind: "bills", Tone: toneBad,
			Text: fmt.Sprintf("%d %s overdue", overdue, plural(overdue, "bill is", "bills are"))})
	case dueCount > 0 && dueTotal > out.Liquid:
		all = append(all, Insight{Kind: "bills", Tone: toneBad,
			Text: fmt.Sprintf("%s due in the next week, more than the %s on hand", amount(dueTotal), amount(out.Liquid))})
	case dueCount > 0:
		all = append(all, Insight{Kind: "bills", Tone: tonePlain,
			Text: fmt.Sprintf("%s due in the next week, across %d %s", amount(dueTotal), dueCount,
				plural(dueCount, "bill", "bills"))})
	}

	// ---- which way the savings rate moved
	if out.Totals.Income > 0 && out.Previous.Income > 0 {
		d := out.Totals.SavingsRate - out.Previous.SavingsRate
		switch {
		case d >= 5:
			all = append(all, Insight{Kind: "savings", Tone: toneGood,
				Text: fmt.Sprintf("Saving %d%% of income, %d points up on last period", out.Totals.SavingsRate, d)})
		case d <= -5:
			all = append(all, Insight{Kind: "savings", Tone: toneWarn,
				Text: fmt.Sprintf("Saving %d%% of income, %d points down on last period", out.Totals.SavingsRate, -d)})
		}
	}

	if len(all) > 3 {
		all = all[:3]
	}
	return all
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
