// Package mcpserver exposes the daemon to coding agents over MCP. The tool
// schemas are the whole introduction an agent gets: descriptions say what a
// tool does, never what the caller should do.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/karamble/omarchy-omabudget/alerts"
)

// Alerts is what the alert tools need from the engine.
type Alerts interface {
	List() []alerts.Trigger
	Arm(t alerts.Trigger) (alerts.Trigger, error)
	Edit(id string, apply func(*alerts.Trigger)) (alerts.Trigger, error)
	Disarm(id string) (bool, error)
}

// errNoAlerts is what every alert tool answers with when the daemon came up
// without an engine, rather than panicking on a nil interface.
var errNoAlerts = errors.New("alerts are not running on this daemon")

// Source is everything the tools read. Functions rather than values, because
// Handler runs before the engine exists.
type Source struct {
	State      func() alerts.Snapshot
	Monitoring func() bool
	Alerts     func() Alerts
	Version    string

	// The ledger, through the same builders the app reads.
	Dashboard      func(ctx context.Context) (any, error)
	Transactions   func(ctx context.Context, q TransactionQuery) (any, error)
	AddTransaction func(ctx context.Context, in map[string]any) (any, error)
	Budget         func(ctx context.Context, period string) (any, error)
	Bills          func(ctx context.Context, days int) (any, error)
	Spending       func(ctx context.Context, period string) (any, error)
}

// TransactionQuery narrows a listing.
type TransactionQuery struct {
	Search   string `json:"q,omitempty" jsonschema:"text matched against description, notes and payee"`
	Kind     string `json:"kind,omitempty" jsonschema:"expense, income or transfer"`
	Account  string `json:"account,omitempty" jsonschema:"account name or id"`
	Category string `json:"category,omitempty" jsonschema:"category id, as listed in the dashboard's cards"`
	From     string `json:"from,omitempty" jsonschema:"YYYY-MM-DD, inclusive"`
	To       string `json:"to,omitempty" jsonschema:"YYYY-MM-DD, inclusive"`
	Limit    int    `json:"limit,omitempty" jsonschema:"at most this many rows, newest first; default 50, max 500"`
}

// errNoLedger answers every ledger tool when the daemon has no ledger open.
var errNoLedger = errors.New("the ledger is not open on this daemon")

func (s Source) alerts() (Alerts, bool) {
	if s.Alerts == nil {
		return nil, false
	}
	a := s.Alerts()
	if a == nil {
		return nil, false
	}
	return a, true
}

type status struct {
	Monitoring bool   `json:"monitoring"`
	Note       string `json:"note,omitempty"`
}

func (s Source) status() status {
	out := status{Monitoring: s.Monitoring == nil || s.Monitoring()}
	if !out.Monitoring {
		out.Note = "alert evaluation is switched off; armed watches will not fire"
	}
	return out
}

func Handler(src Source) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "omabudget",
		Title:   "OMABUDGET: accounts, ledger, budgets and bills",
		Version: src.Version,
	}, nil)
	register(server, src)

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)
}

type empty struct{}

type healthOut struct {
	Status  status `json:"status"`
	Version string `json:"version"`
}

type catalogueOut struct {
	Status status        `json:"status"`
	Leaves []alerts.Leaf `json:"leaves"`
}

type alertsOut struct {
	Status   status           `json:"status"`
	Count    int              `json:"count"`
	Triggers []alerts.Trigger `json:"triggers"`
}

type disarmIn struct {
	ID string `json:"id" jsonschema:"the trigger id to remove"`
}

type addIn struct {
	Amount         string   `json:"amount" jsonschema:"the amount as text in the account's currency, arithmetic allowed: 23.50+18"`
	Category       string   `json:"category,omitempty" jsonschema:"category name or id; Uncategorised when empty"`
	Kind           string   `json:"kind,omitempty" jsonschema:"expense (default), income or transfer"`
	Account        string   `json:"account,omitempty" jsonschema:"account name or id; the first checking account when empty"`
	CounterAccount string   `json:"counterAccount,omitempty" jsonschema:"for a transfer, the account the money goes to"`
	Date           string   `json:"date,omitempty" jsonschema:"YYYY-MM-DD; today when empty"`
	Description    string   `json:"description,omitempty"`
	Notes          string   `json:"notes,omitempty"`
	Tags           []string `json:"tags,omitempty"`
}

type periodIn struct {
	Period string `json:"period,omitempty" jsonschema:"YYYY-MM; the current period when empty"`
}

type billsIn struct {
	Days int `json:"days,omitempty" jsonschema:"how far ahead to look; default 30"`
}

type armIn struct {
	Path      string         `json:"path" jsonschema:"a catalogue path"`
	Operator  string         `json:"operator" jsonschema:"appears, disappears, count, crosses, becomes or ages"`
	Params    alerts.Params  `json:"params,omitempty" jsonschema:"the operator's knobs: above or below for count and crosses, value for becomes, olderThan and field for ages"`
	Where     []alerts.Where `json:"where,omitempty" jsonschema:"filters over a list path, every one must hold: field, op (= or ~=), value"`
	DeliverTo string         `json:"deliverTo,omitempty" jsonschema:"where the alarm goes; \"you\" (the default) is the desktop"`
	ExpiresIn string         `json:"expiresIn,omitempty" jsonschema:"how long the watch stays armed, as a duration such as 24h or 168h; default 168h"`
	Standing  bool           `json:"standing,omitempty" jsonschema:"keep firing each time the condition recurs rather than once"`
	Reason    string         `json:"reason,omitempty" jsonschema:"why the watch exists, shown with the alarm"`
	ArmedBy   string         `json:"armedBy,omitempty" jsonschema:"who is arming it, recorded on the watch"`
}

type editIn struct {
	ID string `json:"id" jsonschema:"the trigger id to change"`
	armIn
}

type armOut struct {
	Status  status         `json:"status"`
	Trigger alerts.Trigger `json:"trigger"`
}

type anyOut struct {
	Status status `json:"status"`
	Result any    `json:"result"`
}

// trigger builds a watch from the tool's input, through the same builder the
// command line and the API use.
func (in armIn) trigger(now time.Time) (alerts.Trigger, error) {
	return alerts.Spec{
		Path: in.Path, Operator: in.Operator, Params: in.Params, Where: in.Where,
		DeliverTo: in.DeliverTo, ExpiresIn: in.ExpiresIn, Standing: in.Standing,
		Reason: in.Reason, ArmedBy: in.ArmedBy,
	}.Trigger(now, "mcp")
}

type disarmOut struct {
	Status   status `json:"status"`
	Disarmed bool   `json:"disarmed"`
}

func register(s *mcp.Server, src Source) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "omabudget_health",
		Description: "Whether the daemon is awake and whether alert evaluation is switched on.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, healthOut, error) {
		return nil, healthOut{Status: src.status(), Version: src.Version}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "omabudget_catalogue",
		Description: "Every path a watch can be armed on: each path, what kind of value it " +
			"yields, the operators it accepts and, for lists, the fields a filter can use.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, catalogueOut, error) {
		return nil, catalogueOut{Status: src.status(), Leaves: alerts.Catalogue()}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "omabudget_alerts",
		Description: "Watches currently armed, with the id, owner and state of each.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, alertsOut, error) {
		e, ok := src.alerts()
		if !ok {
			return nil, alertsOut{}, errNoAlerts
		}
		list := e.List()
		return nil, alertsOut{Status: src.status(), Count: len(list), Triggers: list}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "omabudget_disarm",
		Description: "Remove an armed watch by id. Each watch records an armedBy owner, " +
			"which is reported so a caller can see who armed it.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in disarmIn) (*mcp.CallToolResult, disarmOut, error) {
		e, ok := src.alerts()
		if !ok {
			return nil, disarmOut{}, errNoAlerts
		}
		gone, err := e.Disarm(in.ID)
		if err != nil {
			return nil, disarmOut{}, err
		}
		return nil, disarmOut{Status: src.status(), Disarmed: gone}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "omabudget_arm",
		Description: "Arm a watch on a catalogue path: it fires when the operator's condition " +
			"holds and delivers to the desktop or a named target. Nothing stays armed for ever; " +
			"the watch expires after expiresIn.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in armIn) (*mcp.CallToolResult, armOut, error) {
		e, ok := src.alerts()
		if !ok {
			return nil, armOut{}, errNoAlerts
		}
		t, err := in.trigger(time.Now())
		if err != nil {
			return nil, armOut{}, err
		}
		armed, err := e.Arm(t)
		if err != nil {
			return nil, armOut{}, err
		}
		return nil, armOut{Status: src.status(), Trigger: armed}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "omabudget_edit",
		Description: "Replace an armed watch's terms by id. Its learned state is cleared, so " +
			"the next sample sets a new baseline; id, owner and arming time stay.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, armOut, error) {
		e, ok := src.alerts()
		if !ok {
			return nil, armOut{}, errNoAlerts
		}
		t, err := in.armIn.trigger(time.Now())
		if err != nil {
			return nil, armOut{}, err
		}
		edited, err := e.Edit(in.ID, func(cur *alerts.Trigger) {
			cur.Path, cur.Operator, cur.Params, cur.Where = t.Path, t.Operator, t.Params, t.Where
			cur.DeliverTo, cur.Standing, cur.Reason, cur.ExpiresAt = t.DeliverTo, t.Standing, t.Reason, t.ExpiresAt
		})
		if err != nil {
			return nil, armOut{}, err
		}
		return nil, armOut{Status: src.status(), Trigger: edited}, nil
	})

	// ---- the ledger

	mcp.AddTool(s, &mcp.Tool{
		Name: "omabudget_dashboard",
		Description: "The current period at a glance: totals against the period before, liquid " +
			"funds and net worth, every account with its balance, the twelve periods' totals, the " +
			"budget cards, what is due in the next thirty days and the most recent transactions. " +
			"Amounts are integer minor units of the base currency.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, anyOut, error) {
		if src.Dashboard == nil {
			return nil, anyOut{}, errNoLedger
		}
		out, err := src.Dashboard(ctx)
		if err != nil {
			return nil, anyOut{}, err
		}
		return nil, anyOut{Status: src.status(), Result: out}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "omabudget_transactions",
		Description: "Transactions newest first, narrowed by text, kind, account, category and dates.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TransactionQuery) (*mcp.CallToolResult, anyOut, error) {
		if src.Transactions == nil {
			return nil, anyOut{}, errNoLedger
		}
		out, err := src.Transactions(ctx, in)
		if err != nil {
			return nil, anyOut{}, err
		}
		return nil, anyOut{Status: src.status(), Result: out}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "omabudget_add_transaction",
		Description: "Record an expense, income or transfer. The daemon validates it like an " +
			"entry typed in the app: the category must match the kind, a transfer needs two " +
			"accounts, and the base amount is frozen at entry.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, anyOut, error) {
		if src.AddTransaction == nil {
			return nil, anyOut{}, errNoLedger
		}
		raw, err := json.Marshal(in)
		if err != nil {
			return nil, anyOut{}, err
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, anyOut{}, err
		}
		out, err := src.AddTransaction(ctx, m)
		if err != nil {
			return nil, anyOut{}, err
		}
		return nil, anyOut{Status: src.status(), Result: out}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "omabudget_budget",
		Description: "A period's plan line by line against what was spent, and the spending that has no plan.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in periodIn) (*mcp.CallToolResult, anyOut, error) {
		if src.Budget == nil {
			return nil, anyOut{}, errNoLedger
		}
		out, err := src.Budget(ctx, in.Period)
		if err != nil {
			return nil, anyOut{}, err
		}
		return nil, anyOut{Status: src.status(), Result: out}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "omabudget_bills",
		Description: "What recurring rules will post or ask to be confirmed in the coming days, overdue ones first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in billsIn) (*mcp.CallToolResult, anyOut, error) {
		if src.Bills == nil {
			return nil, anyOut{}, errNoLedger
		}
		days := in.Days
		if days <= 0 {
			days = 30
		}
		out, err := src.Bills(ctx, days)
		if err != nil {
			return nil, anyOut{}, err
		}
		return nil, anyOut{Status: src.status(), Result: out}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "omabudget_spending",
		Description: "Spending by category for a period against the period before: amount, share, change and plan, with each category's lines.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in periodIn) (*mcp.CallToolResult, anyOut, error) {
		if src.Spending == nil {
			return nil, anyOut{}, errNoLedger
		}
		out, err := src.Spending(ctx, in.Period)
		if err != nil {
			return nil, anyOut{}, err
		}
		return nil, anyOut{Status: src.status(), Result: out}, nil
	})
}
