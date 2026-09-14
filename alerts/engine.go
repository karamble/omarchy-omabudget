package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// passEvery is how often armed triggers are folded through the current state.
// This reads memory only: the polls themselves set the pace at which that state
// changes.
const passEvery = 20 * time.Second

// Deliverer carries one alarm to whoever armed it.
type Deliverer func(t Trigger, f Fire) (channel string, err error)

// Engine owns the trigger store and folds each sample through it.
type Engine struct {
	logger  *slog.Logger
	sample  func() Snapshot
	awake   func() bool
	deliver Deliverer

	mu    sync.Mutex
	store *Store
}

// NewEngine builds an engine over a loaded store.
func NewEngine(store *Store, sample func() Snapshot, awake func() bool, deliver Deliverer, logger *slog.Logger) *Engine {
	return &Engine{store: store, sample: sample, awake: awake, deliver: deliver, logger: logger}
}

// Run folds on a timer until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(passEvery)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Pass(time.Now())
		}
	}
}

// Pass evaluates every armed trigger once and delivers whatever tripped.
func (e *Engine) Pass(now time.Time) []Fire {
	// Asleep means asleep. Waking a person or an agent is an outbound act, and
	// the state behind it is stale anyway.
	if e.awake != nil && !e.awake() {
		return nil
	}

	snap := e.sample()

	e.mu.Lock()
	var fires []Fire
	changed := false
	for i := range e.store.Triggers {
		t := &e.store.Triggers[i]
		before := t.State
		got := Evaluate(t, snap, now)
		if len(got) > 0 || !sameState(before, t.State) {
			changed = true
		}
		fires = append(fires, got...)
	}
	// Copy what is needed for delivery before letting go of the lock.
	pending := make([]Trigger, 0, len(fires))
	for _, f := range fires {
		if t, ok := e.store.Find(f.TriggerID); ok {
			pending = append(pending, *t)
		}
	}
	e.mu.Unlock()

	for i, f := range fires {
		if i >= len(pending) {
			break
		}
		e.dispatch(pending[i], f)
		changed = true
	}

	if changed {
		e.save()
	}
	return fires
}

// dispatch delivers one alarm and records how it went.
func (e *Engine) dispatch(t Trigger, f Fire) {
	if e.deliver == nil {
		return
	}
	channel, err := e.deliver(t, f)

	e.mu.Lock()
	defer e.mu.Unlock()
	stored, ok := e.store.Find(t.ID)
	if !ok {
		return
	}
	if err != nil {
		stored.State.DeliveryError = err.Error()
		e.logger.Warn("alarm not delivered", "trigger", t.ID, "err", err)
		return
	}
	stored.State.Delivered = channel
	stored.State.DeliveryError = ""
	e.logger.Info("alarm delivered", "trigger", t.ID, "via", channel, "path", t.Path)
}

// List returns every trigger, newest first.
func (e *Engine) List() []Trigger {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := slices.Clone(e.store.Triggers)
	slices.SortFunc(out, func(a, b Trigger) int { return b.ArmedAt.Compare(a.ArmedAt) })
	return out
}

// Arm validates and stores a trigger, returning it with its id filled in.
func (e *Engine) Arm(t Trigger) (Trigger, error) {
	if err := Validate(t); err != nil {
		return Trigger{}, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if existing, dup := e.store.Duplicate(t); dup {
		return Trigger{}, fmt.Errorf("%s already watches this; edit it instead", existing)
	}

	id, err := NewID()
	if err != nil {
		return Trigger{}, err
	}
	t.ID = id
	t.Rev = 1
	if t.ArmedAt.IsZero() {
		t.ArmedAt = time.Now()
	}
	e.store.Add(t)
	if err := e.store.Save(); err != nil {
		return Trigger{}, err
	}
	e.logger.Info("trigger armed",
		"id", t.ID, "path", t.Path, "operator", t.Operator, "by", t.ArmedBy)
	return t, nil
}

// Edit replaces a trigger's terms and clears its learned state, so the next
// sample sets a new baseline. Id, owner and arming time are kept.
func (e *Engine) Edit(id string, apply func(*Trigger)) (Trigger, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	stored, ok := e.store.Find(id)
	if !ok {
		return Trigger{}, fmt.Errorf("no trigger %q", id)
	}

	next := *stored
	apply(&next)
	next.ID = stored.ID
	next.ArmedBy = stored.ArmedBy
	next.ArmedAt = stored.ArmedAt

	if err := Validate(next); err != nil {
		return Trigger{}, err
	}
	next.Rev = stored.Rev + 1
	next.State = State{}

	*stored = next
	if err := e.store.Save(); err != nil {
		return Trigger{}, err
	}
	e.logger.Info("trigger edited",
		"id", id, "rev", next.Rev, "path", next.Path, "operator", next.Operator)
	return next, nil
}

// Disarm removes a trigger.
func (e *Engine) Disarm(id string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.store.Remove(id) {
		return false, nil
	}
	if err := e.store.Save(); err != nil {
		return false, err
	}
	e.logger.Info("trigger disarmed", "id", id)
	return true, nil
}

func (e *Engine) save() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.store.Save(); err != nil {
		e.logger.Warn("could not persist trigger state", "err", err)
	}
}

// sameState reports whether a pass left a trigger untouched, so an idle daemon
// does not rewrite the store every twenty seconds.
func sameState(a, b State) bool {
	return a.Primed == b.Primed && a.Ready == b.Ready &&
		a.FireCount == b.FireCount &&
		fmt.Sprint(a.LastValue) == fmt.Sprint(b.LastValue) &&
		slices.Equal(a.Seen, b.Seen)
}

// Validate checks a trigger against the catalogue before it is stored, so a
// nonsense watch is refused at arming rather than sitting silent for ever.
func Validate(t Trigger) error {
	leaf, ok := Lookup(t.Path)
	if !ok {
		return fmt.Errorf("unknown path %q: see the catalogue", t.Path)
	}
	if !leaf.Accepts(t.Operator) {
		return fmt.Errorf("%s does not take %s; it takes %v", t.Path, t.Operator, leaf.Operators)
	}
	if t.ExpiresAt.IsZero() {
		return fmt.Errorf("an expiry is required: nothing stays armed for ever")
	}
	if t.DeliverTo == "" {
		return fmt.Errorf("a delivery target is required")
	}

	switch t.Operator {
	case OpCrosses, OpCount:
		if t.Params.Above == nil && t.Params.Below == nil {
			return fmt.Errorf("%s needs --above or --below", t.Operator)
		}
		if t.Params.Above != nil && t.Params.Below != nil {
			return fmt.Errorf("%s takes one bound, not both", t.Operator)
		}
	case OpBecomes:
		if t.Params.Value == "" {
			return fmt.Errorf("becomes needs --value")
		}
	case OpAges:
		if _, err := ParseSpan(t.Params.OlderThan); err != nil {
			return fmt.Errorf("ages needs --older-than: %w", err)
		}
		field := t.Params.Field
		if field == "" && len(leaf.TimeFields) > 0 {
			field = leaf.TimeFields[0]
		}
		if !slices.Contains(leaf.TimeFields, field) {
			return fmt.Errorf("%s has no time field %q; it has %v", t.Path, field, leaf.TimeFields)
		}
	}

	for _, w := range t.Where {
		if len(leaf.Fields) > 0 && !slices.Contains(leaf.Fields, w.Field) {
			return fmt.Errorf("%s has no field %q to filter on; it has %v", t.Path, w.Field, leaf.Fields)
		}
	}
	return nil
}
