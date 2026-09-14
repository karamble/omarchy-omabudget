package domain

import (
	"context"
	"testing"
)

func TestMonthsUntil(t *testing.T) {
	cases := []struct {
		key, due string
		want     int
	}{
		{"2026-09", "2026-09", 1}, {"2026-09", "2026-12", 4}, {"2026-11", "2027-02", 4},
		{"2026-09", "2026-08", 0}, {"2026-09", "soon", 0},
	}
	for _, c := range cases {
		if got := monthsUntil(c.key, c.due); got != c.want {
			t.Errorf("%s to %s: got %d want %d", c.key, c.due, got, c.want)
		}
	}
}

func TestEnvelopesAndRollover(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a := mustAccount(t, l, "Main", Checking, "EUR", 300000)
	mustAccount(t, l, "Broker", Investment, "EUR", 999999) // not liquid
	groceries := mustCategory(t, l, "Groceries")
	fuel := mustCategory(t, l, "Fuel")
	holiday := mustCategory(t, l, "Accommodation")

	// August: groceries monthly, fuel rolls over, a holiday goal.
	if _, err := l.SetCategoryBehaviour(ctx, fuel.ID, BehaviourRollover); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetCategoryGoal(ctx, holiday.ID, 120000, "2026-12"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetCategoryBehaviour(ctx, groceries.ID, BehaviourGoal); err == nil {
		t.Fatal("goal behaviour without a target was accepted")
	}
	for _, b := range []struct {
		id      string
		planned int64
	}{{groceries.ID, 30000}, {fuel.ID, 10000}, {holiday.ID, 20000}} {
		if err := l.SetBudget(ctx, b.id, "2026-08", b.planned); err != nil {
			t.Fatal(err)
		}
	}
	spend(t, l, a, "Groceries", "2026-08-10", 32000) // over by 20
	spend(t, l, a, "Fuel", "2026-08-12", 6000)       // 40 left

	aug, err := l.Envelopes(ctx, "2026-08", "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	if aug.Liquid != 300000-32000-6000 || aug.Assigned != 60000 || aug.Spent != 38000 {
		t.Fatalf("%+v", aug)
	}
	if aug.Held != 4000+20000 || aug.Deficit != 2000 || aug.ToBeBudgeted != aug.Liquid-24000 {
		t.Fatalf("held %d deficit %d tbb %d", aug.Held, aug.Deficit, aug.ToBeBudgeted)
	}
	// Sorted by what is available: the overspent pot first.
	if aug.Items[0].Name != "Groceries" || aug.Items[0].Available != -2000 {
		t.Fatalf("%+v", aug.Items[0])
	}
	var hol Envelope
	for _, e := range aug.Items {
		if e.CategoryID == holiday.ID {
			hol = e
		}
	}
	// 1,200 by December, 200 in the pot, five periods August to December.
	if hol.MonthsLeft != 5 || hol.Accrual != 20000 {
		t.Fatalf("%+v", hol)
	}

	n, err := l.Rollover(ctx, "2026-08", "2026-08-01", "2026-08-31", "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rolled %d lines, want fuel and the goal", n)
	}
	sep, err := l.Envelopes(ctx, "2026-09", "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Envelope{}
	for _, e := range sep.Items {
		got[e.CategoryID] = e
	}
	if _, ok := got[groceries.ID]; ok {
		t.Fatal("a monthly pot rolled over")
	}
	if got[fuel.ID].RolloverIn != 4000 || got[fuel.ID].Available != 4000 || got[holiday.ID].RolloverIn != 20000 {
		t.Fatalf("%+v", got)
	}
	// Assigning zero keeps a line that carries a rollover.
	if err := l.SetBudget(ctx, fuel.ID, "2026-09", 0); err != nil {
		t.Fatal(err)
	}
	sep, _ = l.Envelopes(ctx, "2026-09", "2026-09-01", "2026-09-30")
	found := false
	for _, e := range sep.Items {
		if e.CategoryID == fuel.ID && e.RolloverIn == 4000 {
			found = true
		}
	}
	if !found {
		t.Fatal("the rolled-over fuel line went with its zero assignment")
	}
	// Overspending carries as a deficit.
	spend(t, l, a, "Fuel", "2026-09-03", 9000)
	if _, err := l.Rollover(ctx, "2026-09", "2026-09-01", "2026-09-30", "2026-10"); err != nil {
		t.Fatal(err)
	}
	oct, _ := l.Envelopes(ctx, "2026-10", "2026-10-01", "2026-10-31")
	for _, e := range oct.Items {
		if e.CategoryID == fuel.ID && e.RolloverIn != -5000 {
			t.Fatalf("fuel carried %d, want -5000", e.RolloverIn)
		}
	}
	if oct.Deficit != 5000 {
		t.Fatalf("deficit %d", oct.Deficit)
	}
	if _, err := l.Rollover(ctx, "2026-10", "2026-10-01", "2026-10-31", "2026-09"); err == nil {
		t.Fatal("rolled backwards")
	}

	// Clearing a goal puts the category back to rolling over.
	c, err := l.SetCategoryGoal(ctx, holiday.ID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Behaviour != BehaviourRollover || c.GoalTarget != 0 || c.GoalDue != "" {
		t.Fatalf("%+v", c)
	}
	if _, err := l.SetCategoryGoal(ctx, holiday.ID, 5000, "December"); err == nil {
		t.Fatal("a goal without a month was accepted")
	}
	if _, err := l.SetCategoryGoal(ctx, "Primary salary", 5000, "2026-12"); err == nil {
		t.Fatal("a goal on income was accepted")
	}
}
