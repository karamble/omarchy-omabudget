package api

import (
	"testing"
	"time"
)

// TestSpansContiguous sweeps every start day over three years of
// clocks: twelve periods, each ending the day before the next begins, the
// last one holding today, keys and labels taken from the period's first day.
func TestSpansContiguous(t *testing.T) {
	first := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	last := time.Date(2028, 12, 31, 12, 0, 0, 0, time.UTC)
	for startDay := 1; startDay <= 28; startDay++ {
		for now := first; !now.After(last); now = now.AddDate(0, 0, 1) {
			current := span{periodStart(now, startDay)}
			if now.Before(current.start) || now.After(current.end().Add(24*time.Hour)) {
				t.Fatalf("start %d now %s: period %s..%s does not hold today", startDay, now.Format(dateFmt), current.from(), current.to())
			}
			if current.start.Day() != startDay {
				t.Fatalf("start %d now %s: period begins on %s", startDay, now.Format(dateFmt), current.from())
			}
			spans := spansBack(current, 12)
			if len(spans) != 12 || spans[11] != current {
				t.Fatalf("start %d now %s: %d spans, last %s", startDay, now.Format(dateFmt), len(spans), spans[len(spans)-1].from())
			}
			keys := map[string]bool{}
			for i, sp := range spans {
				if sp.key() != sp.from()[:7] || sp.label() != sp.start.Month().String()[:3] {
					t.Fatalf("start %d: span %s key %s label %s", startDay, sp.from(), sp.key(), sp.label())
				}
				if keys[sp.key()] {
					t.Fatalf("start %d now %s: duplicate key %s", startDay, now.Format(dateFmt), sp.key())
				}
				keys[sp.key()] = true
				if d := sp.days(); d < 28 || d > 31 {
					t.Fatalf("start %d: span %s..%s has %d days", startDay, sp.from(), sp.to(), d)
				}
				if i > 0 && !spans[i-1].end().AddDate(0, 0, 1).Equal(sp.start) {
					t.Fatalf("start %d: gap between %s..%s and %s", startDay, spans[i-1].from(), spans[i-1].to(), sp.from())
				}
				if sp.to() < sp.from() {
					t.Fatalf("start %d: span %s..%s reversed", startDay, sp.from(), sp.to())
				}
			}
			p := current.bounds(now)
			if p.Elapsed < 1 || p.Elapsed > p.Days {
				t.Fatalf("start %d now %s: elapsed %d of %d", startDay, now.Format(dateFmt), p.Elapsed, p.Days)
			}
			// The key resolves back to the same window.
			back, err := spanForKey(current.key(), startDay)
			if err != nil || back.from() != current.from() || back.to() != current.to() {
				t.Fatalf("start %d: key %s resolves to %s..%s (%v), want %s..%s", startDay, current.key(), back.from(), back.to(), err, current.from(), current.to())
			}
		}
	}
}
