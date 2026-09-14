package alerts

import (
	"fmt"
	"slices"
	"time"
)

// Kind is the value type a leaf yields.
type Kind string

const (
	KindNumber Kind = "number"
	KindText   Kind = "text"
	KindBool   Kind = "bool"
	KindList   Kind = "list"
)

// Leaf is one watchable path.
type Leaf struct {
	Path      string     `json:"path"`
	Kind      Kind       `json:"kind"`
	Operators []Operator `json:"operators"`
	Describes string     `json:"describes"`
	// Fields are what --where can filter on, for a list.
	Fields []string `json:"fields,omitempty"`
	// Identity is what makes two entries the same entry, which is what makes
	// appears and disappears honest.
	Identity []string `json:"identity,omitempty"`
	// TimeFields are the timestamps ages can measure.
	TimeFields []string `json:"timeFields,omitempty"`
}

// Accepts reports whether this leaf takes the given operator.
func (l Leaf) Accepts(op Operator) bool { return slices.Contains(l.Operators, op) }

var (
	numberOps = []Operator{OpCrosses}
	textOps   = []Operator{OpBecomes}
	listOps   = []Operator{OpAppears, OpDisappears, OpCount}
	// Lists carrying a timestamp also accept ages.
	listOpsAged = []Operator{OpAppears, OpDisappears, OpCount, OpAges}
)

// Field sets for the list leaves.
var (
	budgetFields   = []string{"category", "planned", "actual", "remaining", "utilisation"}
	budgetIdentity = []string{"category"}

	billFields   = []string{"id", "name", "amount", "category", "account", "due", "daysLeft"}
	billIdentity = []string{"id"}
	billTimes    = []string{"due"}

	txnFields   = []string{"id", "date", "description", "amount", "category", "account", "payee"}
	txnIdentity = []string{"id"}
	txnTimes    = []string{"date"}

	accountFields   = []string{"account", "balance", "threshold", "currency"}
	accountIdentity = []string{"account"}
)

// Catalogue is every path a trigger can watch, following spec section 9.
func Catalogue() []Leaf {
	return []Leaf{
		// ---- budget, as numbers
		{Path: "budget.categoriesAtWarn", Kind: KindNumber, Operators: numberOps,
			Describes: "categories at or past 80 percent of plan this period"},
		{Path: "budget.categoriesOver", Kind: KindNumber, Operators: numberOps,
			Describes: "categories at or past 100 percent of plan this period"},
		{Path: "budget.toBeBudgeted", Kind: KindNumber, Operators: numberOps,
			Describes: "unassigned funds in the envelope model; negative means over-assigned"},
		{Path: "period.spent", Kind: KindNumber, Operators: numberOps,
			Describes: "total expense this period, in base currency minor units"},
		{Path: "period.savingsRate", Kind: KindNumber, Operators: numberOps,
			Describes: "savings rate this period as a percentage"},

		// ---- bills, as numbers
		{Path: "bills.dueCount", Kind: KindNumber, Operators: numberOps,
			Describes: "recurring items due inside their lead time"},
		{Path: "bills.overdueCount", Kind: KindNumber, Operators: numberOps,
			Describes: "recurring items past their due date and not confirmed"},

		// ---- the state of the app itself
		{Path: "health.monitoring", Kind: KindBool, Operators: textOps,
			Describes: "whether alert evaluation is switched on"},

		// ---- the lists, which is where most triggers will live
		{Path: "budget.warn", Kind: KindList, Operators: listOps,
			Describes: "categories at or past 80 percent of plan",
			Fields:    budgetFields, Identity: budgetIdentity},
		{Path: "budget.over", Kind: KindList, Operators: listOps,
			Describes: "categories at or past 100 percent of plan",
			Fields:    budgetFields, Identity: budgetIdentity},
		{Path: "bills.due", Kind: KindList, Operators: listOpsAged,
			Describes: "recurring items due inside their lead time",
			Fields:    billFields, Identity: billIdentity, TimeFields: billTimes},
		{Path: "bills.overdue", Kind: KindList, Operators: listOpsAged,
			Describes: "recurring items past their due date",
			Fields:    billFields, Identity: billIdentity, TimeFields: billTimes},
		{Path: "ledger.large", Kind: KindList, Operators: listOpsAged,
			Describes: "transactions above the configured large-amount threshold",
			Fields:    txnFields, Identity: txnIdentity, TimeFields: txnTimes},
		{Path: "accounts.low", Kind: KindList, Operators: listOps,
			Describes: "flagged accounts whose balance is below their threshold",
			Fields:    accountFields, Identity: accountIdentity},
	}
}

// Lookup finds a leaf by path.
func Lookup(path string) (Leaf, bool) {
	for _, l := range Catalogue() {
		if l.Path == path {
			return l, true
		}
	}
	return Leaf{}, false
}

// Snapshot is everything a trigger is evaluated against. The daemon fills it
// from the ledger; the engine reads it by path.
type Snapshot struct {
	Numbers map[string]float64
	Texts   map[string]string
	Lists   map[string][]map[string]any

	Monitoring bool

	// TakenAt anchors the ages operator.
	TakenAt time.Time
}

// Number resolves a numeric path, reporting false when the path is not one.
func (s Snapshot) Number(path string) (float64, bool) {
	if _, ok := Lookup(path); !ok {
		return 0, false
	}
	v, ok := s.Numbers[path]
	return v, ok
}

// Text resolves a text or bool path as a string.
func (s Snapshot) Text(path string) (string, bool) {
	if path == "health.monitoring" {
		return fmt.Sprint(s.Monitoring), true
	}
	if _, ok := Lookup(path); !ok {
		return "", false
	}
	v, ok := s.Texts[path]
	return v, ok
}

// List resolves a list path into filterable entries.
func (s Snapshot) List(path string) ([]map[string]any, bool) {
	leaf, ok := Lookup(path)
	if !ok || leaf.Kind != KindList {
		return nil, false
	}
	v, ok := s.Lists[path]
	if !ok {
		return []map[string]any{}, true
	}
	return v, true
}

func (s Snapshot) now() time.Time {
	if s.TakenAt.IsZero() {
		return time.Now()
	}
	return s.TakenAt
}

// identityOf joins the identity fields of an entry into one key.
func identityOf(entry map[string]any, fields []string) string {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, fmt.Sprint(entry[f]))
	}
	return fmt.Sprint(parts)
}

// filter keeps the entries every where clause matches.
func filter(entries []map[string]any, wheres []Where) []map[string]any {
	if len(wheres) == 0 {
		return entries
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		keep := true
		for _, w := range wheres {
			if !w.Match(e) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, e)
		}
	}
	return out
}
