// Package alerts holds armed watches over the sentinel's own view: an agent says
// "wake me when this happens", goes away, and is woken with the reason it
// cared. The alarm carries no values, so whoever is woken reads the sentinel.
package alerts

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/store"
)

// Operator is how a path is watched. Six, chosen for this data: almost
// everything here is a list of things that arrive and leave.
type Operator string

const (
	// OpAppears fires for each entry that was not in the previous sample.
	OpAppears Operator = "appears"
	// OpDisappears fires for each entry that has gone.
	OpDisappears Operator = "disappears"
	// OpCount watches how many entries survive the filter, crossing a bound.
	OpCount Operator = "count"
	// OpCrosses fires on the sample that carries a number past a bound.
	OpCrosses Operator = "crosses"
	// OpBecomes fires when text or a bool takes a value it did not have.
	OpBecomes Operator = "becomes"
	// OpAges fires for an entry whose timestamp is older than a span. This is
	// the staleness that matters here: a pull request going quiet is a property
	// of one entry, not of a count.
	OpAges Operator = "ages"
)

// Operators lists every operator, for validation and for the catalogue.
var Operators = []Operator{OpAppears, OpDisappears, OpCount, OpCrosses, OpBecomes, OpAges}

// ParseOperator maps a name to an Operator.
func ParseOperator(name string) (Operator, bool) {
	op := Operator(strings.TrimSpace(name))
	if slices.Contains(Operators, op) {
		return op, true
	}
	return "", false
}

// Params carries whichever knobs the chosen operator needs.
type Params struct {
	Above *float64 `json:"above,omitempty"`
	Below *float64 `json:"below,omitempty"`
	Value string   `json:"value,omitempty"`
	// Rearm is how far back past the bound a value must come before the
	// trigger can fire again. Defaults per operator.
	Rearm *float64 `json:"rearm,omitempty"`
	// Hold is how long a becomes value must stay away before it rings again,
	// so a flapping status rings once.
	Hold string `json:"hold,omitempty"`
	// Field and OlderThan belong to ages.
	Field     string `json:"field,omitempty"`
	OlderThan string `json:"olderThan,omitempty"`
}

// Where is one filter over a list, either an exact match or a case-insensitive
// substring.
type Where struct {
	Field string `json:"field"`
	// Op is "=" for equality or "~=" for substring.
	Op    string `json:"op"`
	Value string `json:"value"`
}

// Match reports whether one entry satisfies this filter.
func (w Where) Match(entry map[string]any) bool {
	raw, ok := entry[w.Field]
	if !ok {
		return false
	}
	got := fmt.Sprint(raw)
	if w.Op == "~=" {
		return strings.Contains(strings.ToLower(got), strings.ToLower(w.Value))
	}
	return got == w.Value
}

// ParseWhere reads "field=value" or "field~=text".
func ParseWhere(spec string) (Where, error) {
	if field, value, found := strings.Cut(spec, "~="); found {
		field = strings.TrimSpace(field)
		if field == "" {
			return Where{}, fmt.Errorf("filter %q has no field", spec)
		}
		return Where{Field: field, Op: "~=", Value: strings.TrimSpace(value)}, nil
	}
	field, value, found := strings.Cut(spec, "=")
	if !found || strings.TrimSpace(field) == "" {
		return Where{}, fmt.Errorf("filter %q must be field=value or field~=text", spec)
	}
	return Where{Field: strings.TrimSpace(field), Op: "=", Value: strings.TrimSpace(value)}, nil
}

// ParseWhereAll reads a repeatable filter, every clause of which must hold.
// The command line and the MCP surface both come through here, so the two
// cannot disagree about what a filter means.
func ParseWhereAll(raw []string) ([]Where, error) {
	out := make([]Where, 0, len(raw))
	for _, spec := range raw {
		w, err := ParseWhere(spec)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// State is what the engine remembers between samples.
type State struct {
	LastValue     any       `json:"lastValue,omitempty"`
	LastChangedAt time.Time `json:"lastChangedAt,omitzero"`
	RefValue      any       `json:"refValue,omitempty"`
	// Ready is false while a fired trigger waits for the condition to be
	// clearly false again.
	Ready bool `json:"ready"`
	// Seen holds the entry identities from the previous sample, which is what
	// makes appears and disappears possible.
	Seen []string `json:"seen,omitempty"`
	// AwaySince is when a becomes value last left its target. Hold is measured
	// from here, not from the last change.
	AwaySince     time.Time `json:"awaySince,omitzero"`
	FiredAt       time.Time `json:"firedAt,omitzero"`
	FireCount     int       `json:"fireCount,omitempty"`
	Delivered     string    `json:"delivered,omitempty"`
	DeliveryError string    `json:"deliveryError,omitempty"`
	// Primed is false until the first sample has been folded in. The first
	// sample sets the baseline and never fires.
	Primed bool `json:"primed"`
}

// Trigger is one armed watch.
type Trigger struct {
	ID       string   `json:"id"`
	Rev      int      `json:"rev"`
	Path     string   `json:"path"`
	Operator Operator `json:"operator"`
	Params   Params   `json:"params"`
	Where    []Where  `json:"where,omitempty"`
	// DeliverTo is a herdr pane or agent name, "repo" to reach whichever agent
	// sits in the checkout concerned, or "you" for the desktop.
	DeliverTo string    `json:"deliverTo"`
	Standing  bool      `json:"standing,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
	Reason    string    `json:"reason,omitempty"`
	ArmedBy   string    `json:"armedBy"`
	ArmedAt   time.Time `json:"armedAt"`
	State     State     `json:"state"`
}

// Status is the one word the panel and the tools use for a trigger.
type Status string

const (
	StatusArmed       Status = "armed"
	StatusRearming    Status = "rearming"
	StatusFired       Status = "fired"
	StatusExpired     Status = "expired"
	StatusNoSample    Status = "no-sample"
	StatusUndelivered Status = "delivery-failed"
)

// Status reports where a trigger stands, as of now.
func (t Trigger) Status(now time.Time) Status {
	switch {
	case t.State.DeliveryError != "":
		return StatusUndelivered
	case !t.Standing && t.State.FireCount > 0:
		return StatusFired
	case now.After(t.ExpiresAt):
		return StatusExpired
	case !t.State.Primed:
		return StatusNoSample
	case !t.State.Ready:
		return StatusRearming
	}
	return StatusArmed
}

// Spent reports whether a trigger can never fire again and is only being kept
// so somebody can see what happened.
func (t Trigger) Spent(now time.Time) bool {
	s := t.Status(now)
	return s == StatusFired || s == StatusExpired
}

// NewID mints a short, readable trigger id.
func NewID() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating id: %w", err)
	}
	return "t-" + hex.EncodeToString(buf), nil
}

// ParseSpan reads "4d", "12h", "90m" or "30s".
func ParseSpan(span string) (time.Duration, error) {
	s := strings.TrimSpace(span)
	if s == "" {
		return 0, errors.New("empty span")
	}
	if strings.HasSuffix(s, "d") {
		var days float64
		if _, err := fmt.Sscanf(s, "%fd", &days); err != nil || days <= 0 {
			return 0, fmt.Errorf("bad span %q", span)
		}
		return time.Duration(days * 24 * float64(time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("bad span %q: use 4d, 12h, 90m or 30s", span)
	}
	return d, nil
}

// ParseExpiry reads a span, a bare date meaning the end of that local day, or
// an RFC 3339 time.
func ParseExpiry(spec string, now time.Time) (time.Time, error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		return time.Time{}, errors.New("an expiry is required: nothing stays armed for ever")
	}
	if d, err := ParseSpan(s); err == nil {
		return now.Add(d), nil
	}
	if day, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return day.Add(24*time.Hour - time.Second), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("bad expiry %q: use 4d, 2026-10-01 or an RFC 3339 time", spec)
}

// Store is the trigger file. It sits beside the account store and is written
// the same way: 0600, atomically.
type Store struct {
	Version  int       `json:"version"`
	Triggers []Trigger `json:"triggers"`

	path string
}

// DefaultPath honours XDG_CONFIG_HOME and falls back to ~/.config/sitesentinel.
func DefaultPath() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "omabudget", "triggers.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "omabudget", "triggers.json")
	}
	return filepath.Join(home, ".config", "omabudget", "triggers.json")
}

// Load reads the trigger store. A missing file is an empty store, not an error:
// having armed nothing yet is the normal state.
func Load(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	dir, err := store.Shared(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	raw, err := dir.Read(filepath.Base(path), 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		return &Store{Version: 1, path: path}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var s Store
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	s.path = path
	if s.Version == 0 {
		s.Version = 1
	}
	return &s, nil
}

// Save writes the store atomically at 0600.
func (s *Store) Save() error {
	if s.path == "" {
		s.path = DefaultPath()
	}
	if s.Version == 0 {
		s.Version = 1
	}

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding triggers: %w", err)
	}
	raw = append(raw, '\n')

	dir, err := store.Shared(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	return dir.Write(filepath.Base(s.path), raw, 0o600)
}

// Path reports where the store lives.
func (s *Store) Path() string {
	if s.path == "" {
		return DefaultPath()
	}
	return s.path
}

// SetPath overrides the location, for tests.
func (s *Store) SetPath(path string) { s.path = path }

// Find returns the trigger with the given id.
func (s *Store) Find(id string) (*Trigger, bool) {
	for i := range s.Triggers {
		if s.Triggers[i].ID == id {
			return &s.Triggers[i], true
		}
	}
	return nil, false
}

// Add stores a new trigger.
func (s *Store) Add(t Trigger) { s.Triggers = append(s.Triggers, t) }

// Remove deletes a trigger, reporting whether it was there.
func (s *Store) Remove(id string) bool {
	i := slices.IndexFunc(s.Triggers, func(t Trigger) bool { return t.ID == id })
	if i < 0 {
		return false
	}
	s.Triggers = slices.Delete(s.Triggers, i, i+1)
	return true
}

// Duplicate reports an existing trigger watching the same thing the same way,
// so arming a second copy can be refused rather than quietly doubling the noise.
func (s *Store) Duplicate(t Trigger) (string, bool) {
	for _, existing := range s.Triggers {
		if existing.Path != t.Path || existing.Operator != t.Operator {
			continue
		}
		if existing.Params != t.Params {
			continue
		}
		if !slices.Equal(existing.Where, t.Where) {
			continue
		}
		return existing.ID, true
	}
	return "", false
}

// Spec is a watch as it is asked for, before it is validated and stored. The
// command line, the HTTP API and the agent tools all arrive here, so none of
// them can mean something different by the same words.
type Spec struct {
	Path      string  `json:"path"`
	Operator  string  `json:"operator"`
	Params    Params  `json:"params,omitempty"`
	Where     []Where `json:"where,omitempty"`
	DeliverTo string  `json:"deliverTo,omitempty"`
	ExpiresIn string  `json:"expiresIn,omitempty"`
	Standing  bool    `json:"standing,omitempty"`
	Reason    string  `json:"reason,omitempty"`
	ArmedBy   string  `json:"armedBy,omitempty"`
}

// DefaultExpiry is how long a watch stays armed when no span is given.
// Nothing stays armed for ever.
const DefaultExpiry = 168 * time.Hour

// Trigger turns a spec into a watch, filling in what was left out. It does
// not validate the terms: Arm does that, and says what is wrong.
func (s Spec) Trigger(now time.Time, by string) (Trigger, error) {
	op, ok := ParseOperator(s.Operator)
	if !ok {
		return Trigger{}, fmt.Errorf("unknown operator %q: %v", s.Operator, Operators)
	}
	t := Trigger{
		Path: strings.TrimSpace(s.Path), Operator: op, Params: s.Params, Where: s.Where,
		DeliverTo: s.DeliverTo, Standing: s.Standing, Reason: s.Reason, ArmedBy: s.ArmedBy,
	}
	if t.DeliverTo == "" {
		t.DeliverTo = "you"
	}
	if t.ArmedBy == "" {
		t.ArmedBy = by
	}
	span := DefaultExpiry
	if s.ExpiresIn != "" {
		d, err := time.ParseDuration(s.ExpiresIn)
		if err != nil || d <= 0 {
			return Trigger{}, fmt.Errorf("expiresIn %q: a positive duration such as 24h or 168h", s.ExpiresIn)
		}
		span = d
	}
	t.ExpiresAt = now.Add(span)
	return t, nil
}
