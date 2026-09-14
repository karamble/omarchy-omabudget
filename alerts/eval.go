package alerts

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// defaultRearm is how far back past a bound a value must come before the same
// trigger can fire again. Every number in this catalogue is a count, so one
// whole unit is the honest default.
const defaultRearm = 1.0

// defaultHold is how long a becomes value must stay away before it rings again,
// so a flapping status rings once.
const defaultHold = 5 * time.Minute

// Fire is one thing that tripped. It carries identity and nothing else: the
// alarm says the condition happened, not what the value is now, so whoever is
// woken reads the sentinel themselves.
type Fire struct {
	TriggerID string         `json:"triggerId"`
	Path      string         `json:"path"`
	Operator  Operator       `json:"operator"`
	Summary   string         `json:"summary"`
	Reason    string         `json:"reason,omitempty"`
	At        time.Time      `json:"at"`
	Entry     map[string]any `json:"-"`
}

// Evaluate folds one sample through one trigger and returns whatever tripped.
// A spent, expired or sampleless trigger returns nothing and keeps its state.
func Evaluate(t *Trigger, snap Snapshot, now time.Time) []Fire {
	if t == nil || t.Spent(now) {
		return nil
	}

	switch t.Operator {
	case OpCrosses:
		return evalCrosses(t, snap, now)
	case OpCount:
		return evalCount(t, snap, now)
	case OpBecomes:
		return evalBecomes(t, snap, now)
	case OpAppears, OpDisappears, OpAges:
		return evalSet(t, snap, now)
	}
	return nil
}

// bound returns the configured bound and which side it is watched from.
func bound(p Params) (value float64, above bool, ok bool) {
	if p.Above != nil {
		return *p.Above, true, true
	}
	if p.Below != nil {
		return *p.Below, false, true
	}
	return 0, false, false
}

func rearmMargin(p Params) float64 {
	if p.Rearm != nil && *p.Rearm > 0 {
		return *p.Rearm
	}
	return defaultRearm
}

// crossing is the shared body of crosses and count.
func crossing(t *Trigger, value float64, now time.Time, describe func(float64) string) []Fire {
	limit, above, ok := bound(t.Params)
	if !ok {
		return nil
	}

	prev, hadPrev := t.State.LastValue.(float64)
	t.State.LastValue = value

	// The first sample sets the baseline and never fires.
	if !t.State.Primed || !hadPrev {
		t.State.Primed = true
		t.State.Ready = true
		t.State.LastChangedAt = now
		return nil
	}
	if prev != value {
		t.State.LastChangedAt = now
	}

	margin := rearmMargin(t.Params)
	if !t.State.Ready {
		// Re-arm only once the value is clearly back on the far side. The
		// re-arming sample itself never fires.
		if (above && value <= limit-margin) || (!above && value >= limit+margin) {
			t.State.Ready = true
		}
		return nil
	}

	crossed := (above && prev <= limit && value > limit) ||
		(!above && prev >= limit && value < limit)
	if !crossed {
		return nil
	}

	t.State.Ready = false
	t.State.FiredAt = now
	t.State.FireCount++
	return []Fire{{
		TriggerID: t.ID, Path: t.Path, Operator: t.Operator,
		Summary: describe(limit), Reason: t.Reason, At: now,
	}}
}

func evalCrosses(t *Trigger, snap Snapshot, now time.Time) []Fire {
	value, ok := snap.Number(t.Path)
	if !ok {
		return nil
	}
	return crossing(t, value, now, func(limit float64) string {
		return fmt.Sprintf("%s crossed %s %s", t.Path, side(t.Params), trim(limit))
	})
}

func evalCount(t *Trigger, snap Snapshot, now time.Time) []Fire {
	entries, ok := snap.List(t.Path)
	if !ok {
		return nil
	}
	n := float64(len(filter(entries, t.Where)))
	return crossing(t, n, now, func(limit float64) string {
		return fmt.Sprintf("the number of %s%s crossed %s %s",
			t.Path, whereSuffix(t.Where), side(t.Params), trim(limit))
	})
}

func evalBecomes(t *Trigger, snap Snapshot, now time.Time) []Fire {
	value, ok := snap.Text(t.Path)
	if !ok {
		return nil
	}

	prev, hadPrev := t.State.LastValue.(string)
	t.State.LastValue = value

	if !t.State.Primed || !hadPrev {
		t.State.Primed = true
		t.State.Ready = true
		t.State.LastChangedAt = now
		return nil
	}
	if prev != value {
		t.State.LastChangedAt = now
	}

	// Track when the value left its target, so the re-arm can be judged from
	// there rather than from this sample.
	if prev == t.Params.Value && value != t.Params.Value {
		t.State.AwaySince = now
	}

	if !t.State.Ready {
		hold := defaultHold
		if d, err := ParseSpan(t.Params.Hold); err == nil {
			hold = d
		}
		// It re-arms once it has been away long enough, judged on the previous
		// value: this sample may already be the return.
		away := t.State.AwaySince
		if prev != t.Params.Value && !away.IsZero() && now.Sub(away) >= hold {
			t.State.Ready = true
		}
	}
	if !t.State.Ready {
		return nil
	}

	if value != t.Params.Value || prev == t.Params.Value {
		return nil
	}

	t.State.Ready = false
	t.State.FiredAt = now
	t.State.FireCount++
	return []Fire{{
		TriggerID: t.ID, Path: t.Path, Operator: t.Operator,
		Summary: fmt.Sprintf("%s became %q", t.Path, t.Params.Value),
		Reason:  t.Reason, At: now,
	}}
}

// evalSet drives appears, disappears and ages, which are the same machinery
// over a different set: what is present, and what is old.
func evalSet(t *Trigger, snap Snapshot, now time.Time) []Fire {
	entries, ok := snap.List(t.Path)
	if !ok {
		return nil
	}
	leaf, ok := Lookup(t.Path)
	if !ok {
		return nil
	}

	kept := filter(entries, t.Where)
	if t.Operator == OpAges {
		kept = agedOnly(kept, leaf, t.Params, now)
	}

	current := make([]string, 0, len(kept))
	byID := make(map[string]map[string]any, len(kept))
	for _, e := range kept {
		id := identityOf(e, leaf.Identity)
		current = append(current, id)
		byID[id] = e
	}
	slices.Sort(current)

	previous := t.State.Seen
	t.State.Seen = current

	if !t.State.Primed {
		t.State.Primed = true
		t.State.Ready = true
		t.State.LastChangedAt = now
		return nil
	}

	var changed []string
	switch t.Operator {
	case OpDisappears:
		for _, id := range previous {
			if !slices.Contains(current, id) {
				changed = append(changed, id)
			}
		}
	default: // appears and ages
		for _, id := range current {
			if !slices.Contains(previous, id) {
				changed = append(changed, id)
			}
		}
	}
	if len(changed) == 0 {
		return nil
	}
	t.State.LastChangedAt = now

	// Every entry is its own event, so these never disarm; a one-shot is spent
	// by the first one.
	fires := make([]Fire, 0, len(changed))
	for _, id := range changed {
		t.State.FiredAt = now
		t.State.FireCount++
		fires = append(fires, Fire{
			TriggerID: t.ID, Path: t.Path, Operator: t.Operator,
			Summary: describeEntry(t, leaf, id, byID[id]),
			Reason:  t.Reason, At: now, Entry: byID[id],
		})
		if !t.Standing {
			break
		}
	}
	return fires
}

// agedOnly keeps the entries whose timestamp is older than the configured span.
func agedOnly(entries []map[string]any, leaf Leaf, p Params, now time.Time) []map[string]any {
	span, err := ParseSpan(p.OlderThan)
	if err != nil {
		return nil
	}
	field := p.Field
	if field == "" && len(leaf.TimeFields) > 0 {
		field = leaf.TimeFields[0]
	}
	if field == "" {
		return nil
	}

	var out []map[string]any
	for _, e := range entries {
		ts, ok := e[field].(time.Time)
		if !ok || ts.IsZero() {
			continue
		}
		if now.Sub(ts) >= span {
			out = append(out, e)
		}
	}
	return out
}

// describeEntry names what tripped by its identity, which is the least that is
// still actionable.
func describeEntry(t *Trigger, leaf Leaf, id string, entry map[string]any) string {
	parts := make([]string, 0, len(leaf.Identity))
	for i, f := range leaf.Identity {
		var value string
		if entry != nil {
			value = fmt.Sprint(entry[f])
		} else if bits := strings.Split(id, "\x1f"); i < len(bits) {
			value = bits[i]
		}
		parts = append(parts, f+"="+value)
	}
	what := strings.Join(parts, " ")

	switch t.Operator {
	case OpDisappears:
		return fmt.Sprintf("an entry left %s%s: %s", t.Path, whereSuffix(t.Where), what)
	case OpAges:
		return fmt.Sprintf("an entry in %s%s has been untouched for %s: %s",
			t.Path, whereSuffix(t.Where), t.Params.OlderThan, what)
	}
	return fmt.Sprintf("a new entry in %s%s: %s", t.Path, whereSuffix(t.Where), what)
}

func side(p Params) string {
	if p.Below != nil {
		return "below"
	}
	return "above"
}

func whereSuffix(wheres []Where) string {
	if len(wheres) == 0 {
		return ""
	}
	parts := make([]string, 0, len(wheres))
	for _, w := range wheres {
		parts = append(parts, w.Field+w.Op+w.Value)
	}
	return " where " + strings.Join(parts, " and ")
}

func trim(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.4f", v), "0"), ".")
}
