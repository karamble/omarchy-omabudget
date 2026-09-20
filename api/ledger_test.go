package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karamble/omarchy-omabudget/config"
	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/money"
)

// newTestServer opens a temp ledger behind a server whose period begins on
// startDay, with the clock pinned to 2026-09-13.
func newTestServer(t *testing.T, startDay int) (*Server, *domain.Ledger) {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "ledger.db"), db.Options{RateReference: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	l := domain.New(d)
	cfg := &config.Config{BaseCurrency: "EUR", PeriodStartDay: startDay, APIToken: "test-token"}
	// A test config saves into the temp dir, never the real one.
	cfg.SetPath(filepath.Join(t.TempDir(), "config.json"))
	s := NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	s.SetLedger(l)
	prev := clock
	clock = func() time.Time { return time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { clock = prev })
	return s, l
}

// call sends one request through the whole handler chain, token included.
func call(t *testing.T, s *Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func mustAccount(t *testing.T, l *domain.Ledger, name string, typ domain.AccountType, opening int64) domain.Account {
	t.Helper()
	a, err := l.AddAccount(context.Background(), domain.Account{
		Name: name, Type: typ, Currency: "EUR", OpeningBalance: opening, OpeningDate: "2026-01-01", IncludeInNetWorth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustAdd(t *testing.T, l *domain.Ledger, tx domain.Transaction) {
	t.Helper()
	if _, err := l.Add(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
}

// TestDashboardDocument checks the shape and the figures of one document:
// every key present, twelve months, previous equal to the eleventh, the last
// cash point equal to liquid, and recent rows carrying their category.
func TestDashboardDocument(t *testing.T) {
	s, l := newTestServer(t, 1)
	main := mustAccount(t, l, "Main", domain.Checking, 100000)
	broker := mustAccount(t, l, "Broker", domain.Investment, 0)
	mustAdd(t, l, domain.Transaction{Kind: domain.Income, AccountID: main.ID, Amount: 250000, CategoryID: "Primary salary", Date: "2026-08-15"})
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: main.ID, Amount: 40000, CategoryID: "Rent", Date: "2026-08-16"})
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: main.ID, Amount: 2500, CategoryID: "Groceries", Date: "2026-09-02"})
	mustAdd(t, l, domain.Transaction{Kind: domain.Income, AccountID: main.ID, Amount: 300000, CategoryID: "Primary salary", Date: "2026-09-03"})
	mustAdd(t, l, domain.Transaction{Kind: domain.Transfer, AccountID: main.ID, CounterAccountID: broker.ID, Amount: 30000, Date: "2026-09-08"})
	if code, body := call(t, s, "PUT", "/api/budgets", map[string]string{"category": "Groceries", "planned": "200"}); code != http.StatusOK {
		t.Fatalf("PUT /api/budgets = %d %s", code, body)
	}

	code, body := call(t, s, "GET", "/api/dashboard", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/dashboard = %d %s", code, body)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"baseCurrency", "period", "totals", "previous", "liquid", "netWorth", "accounts", "recent", "cash", "budget", "months", "today", "rates"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("document lacks %q", k)
		}
	}
	var out dashboardOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}

	if out.Period != (period{From: "2026-09-01", To: "2026-09-30", Days: 30, Elapsed: 13}) {
		t.Errorf("period = %+v", out.Period)
	}
	// The forms preselect this account and label the amount with its
	// currency, so the document has to name the same one the daemon would
	// have picked for itself.
	if out.DefaultAccountID != s.defaultAccount(context.Background(), l) {
		t.Errorf("defaultAccountId = %q, want the account the daemon picks", out.DefaultAccountID)
	}
	if out.DefaultAccountID != main.ID {
		t.Errorf("defaultAccountId = %q, want the checking account %q", out.DefaultAccountID, main.ID)
	}
	if out.Today != "2026-09-13" {
		t.Errorf("today = %q", out.Today)
	}
	// The document carries the newest rate per currency, not the table.
	for _, r := range []struct{ currency, rate, date string }{{"USD", "0.90", "2026-06-01"}, {"USD", "0.95", "2026-09-01"}} {
		if _, err := l.SetRate(context.Background(), r.currency, money.Rate(r.rate), r.date); err != nil {
			t.Fatal(err)
		}
	}
	_, body = call(t, s, "GET", "/api/dashboard", nil)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rates) != 1 || out.Rates[0].Currency != "USD" || out.Rates[0].Date != "2026-09-01" || out.Rates[0].Rate != "0.95" {
		t.Errorf("rates = %+v, want the newest USD rate alone", out.Rates)
	}
	if want := (domain.Totals{Income: 300000, Expense: 2500, Net: 297500, SavingsRate: 99}); out.Totals != want {
		t.Errorf("totals = %+v, want %+v", out.Totals, want)
	}
	if want := (domain.Totals{Income: 250000, Expense: 40000, Net: 210000, SavingsRate: 84}); out.Previous != want {
		t.Errorf("previous = %+v, want %+v", out.Previous, want)
	}
	if out.Liquid != 577500 || out.NetWorth != 607500 {
		t.Errorf("liquid %d net worth %d, want 577500 and 607500", out.Liquid, out.NetWorth)
	}

	if len(out.Months) != 12 {
		t.Fatalf("months = %d entries, want 12", len(out.Months))
	}
	if m := out.Months[0]; m.Key != "2025-10" || m.Label != "Oct" || m.From != "2025-10-01" || m.To != "2025-10-31" {
		t.Errorf("months[0] = %+v", m)
	}
	if m := out.Months[11]; m.Key != "2026-09" || m.Label != "Sep" || m.From != "2026-09-01" || m.To != "2026-09-30" || m.Totals != out.Totals {
		t.Errorf("months[11] = %+v", m)
	}
	if out.Months[10].Totals != out.Previous {
		t.Errorf("months[10] = %+v, previous = %+v", out.Months[10].Totals, out.Previous)
	}

	if n := len(out.Cash.Series); n != 13 {
		t.Fatalf("cash series has %d points, want 13", n)
	}
	if first := out.Cash.Series[0]; first.Date != "2026-09-01" || first.Liquid != 310000 {
		t.Errorf("first cash point = %+v", first)
	}
	if last := out.Cash.Series[12]; last.Date != "2026-09-13" || last.Liquid != out.Liquid {
		t.Errorf("last cash point = %+v, liquid = %d", last, out.Liquid)
	}
	if out.Cash.Delta != 267500 || out.Cash.DeltaPct == nil || *out.Cash.DeltaPct != 86.3 {
		t.Errorf("cash delta = %d pct %v, want 267500 and 86.3", out.Cash.Delta, out.Cash.DeltaPct)
	}

	if b := out.Budget; b.Period != "2026-09" || b.Planned != 20000 || b.Spent != 2500 || b.Pct != 12 || len(b.Cards) != 1 {
		t.Fatalf("budget = %+v", b)
	}
	if c := out.Budget.Cards[0]; c.CategoryID != "food/groceries" || c.Name != "Groceries" || c.Icon != "󰄛" ||
		c.Remaining != 17500 || c.Pct != 12 || c.State != domain.BudgetOK {
		t.Errorf("budget card = %+v", c)
	}

	if len(out.Recent) != 5 {
		t.Fatalf("recent has %d rows, want 5", len(out.Recent))
	}
	if r := out.Recent[0]; r.Kind != domain.Transfer || r.CategoryName != "" || r.CategoryIcon != "" {
		t.Errorf("transfer row = %+v", r)
	}
	if r := out.Recent[1]; r.CategoryName != "Primary salary" || r.CategoryIcon != "󰄔" {
		t.Errorf("salary row = %+v", r)
	}
	if r := out.Recent[2]; r.CategoryName != "Groceries" || r.CategoryIcon != "󰄛" {
		t.Errorf("groceries row = %+v", r)
	}
}

// TestDashboardEmpty: a fresh ledger still answers a complete document with
// empty lists rather than nulls.
func TestDashboardEmpty(t *testing.T) {
	s, _ := newTestServer(t, 1)
	code, body := call(t, s, "GET", "/api/dashboard", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/dashboard = %d %s", code, body)
	}
	text := string(body)
	for _, want := range []string{`"accounts":[]`, `"recent":[]`, `"cards":[]`, `"delta":0`} {
		if !strings.Contains(text, want) {
			t.Errorf("document lacks %s: %s", want, text)
		}
	}
	if strings.Contains(text, "deltaPct") {
		t.Errorf("deltaPct present with nothing to compare against: %s", text)
	}
	var out dashboardOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Months) != 12 || len(out.Cash.Series) != 13 {
		t.Errorf("months %d, cash points %d", len(out.Months), len(out.Cash.Series))
	}
}

// TestMonthsFollowStartDay: with the period beginning on the 10th every
// month runs from the 10th to the 9th, the series crosses the year boundary,
// and spending on the 5th lands in the previous period.
func TestMonthsFollowStartDay(t *testing.T) {
	s, l := newTestServer(t, 10)
	main := mustAccount(t, l, "Main", domain.Checking, 0)
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: main.ID, Amount: 1000, CategoryID: "Groceries", Date: "2026-09-05"})
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: main.ID, Amount: 2000, CategoryID: "Groceries", Date: "2026-09-12"})

	code, body := call(t, s, "GET", "/api/dashboard", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/dashboard = %d %s", code, body)
	}
	var out dashboardOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Period != (period{From: "2026-09-10", To: "2026-10-09", Days: 30, Elapsed: 4}) {
		t.Errorf("period = %+v", out.Period)
	}
	want := []struct{ key, label, from, to string }{
		{"2025-10", "Oct", "2025-10-10", "2025-11-09"},
		{"2025-11", "Nov", "2025-11-10", "2025-12-09"},
		{"2025-12", "Dec", "2025-12-10", "2026-01-09"},
		{"2026-01", "Jan", "2026-01-10", "2026-02-09"},
		{"2026-02", "Feb", "2026-02-10", "2026-03-09"},
		{"2026-03", "Mar", "2026-03-10", "2026-04-09"},
		{"2026-04", "Apr", "2026-04-10", "2026-05-09"},
		{"2026-05", "May", "2026-05-10", "2026-06-09"},
		{"2026-06", "Jun", "2026-06-10", "2026-07-09"},
		{"2026-07", "Jul", "2026-07-10", "2026-08-09"},
		{"2026-08", "Aug", "2026-08-10", "2026-09-09"},
		{"2026-09", "Sep", "2026-09-10", "2026-10-09"},
	}
	if len(out.Months) != len(want) {
		t.Fatalf("months = %d entries, want %d", len(out.Months), len(want))
	}
	for i, w := range want {
		m := out.Months[i]
		if m.Key != w.key || m.Label != w.label || m.From != w.from || m.To != w.to {
			t.Errorf("months[%d] = %s %s %s..%s, want %s %s %s..%s", i, m.Key, m.Label, m.From, m.To, w.key, w.label, w.from, w.to)
		}
	}
	if out.Months[10].Expense != 1000 || out.Previous.Expense != 1000 {
		t.Errorf("the 5th should fall in the previous period: months[10] %+v previous %+v", out.Months[10].Totals, out.Previous)
	}
	if out.Months[11].Expense != 2000 || out.Totals.Expense != 2000 {
		t.Errorf("the 12th should fall in this period: months[11] %+v totals %+v", out.Months[11].Totals, out.Totals)
	}
	if n := len(out.Cash.Series); n != 4 || out.Cash.Series[0].Date != "2026-09-10" {
		t.Errorf("cash series = %+v", out.Cash.Series)
	}
	if out.Budget.Period != "2026-09" {
		t.Errorf("budget period = %q, want 2026-09", out.Budget.Period)
	}

	// A budget for August is read over August's window, the 10th to the 9th.
	for _, p := range []string{"2026-08", "2026-09"} {
		if code, body := call(t, s, "PUT", "/api/budgets", map[string]string{"category": "Groceries", "period": p, "planned": "100"}); code != http.StatusOK {
			t.Fatalf("PUT /api/budgets %s = %d %s", p, code, body)
		}
	}
	var august, september domain.Budget
	if _, body := call(t, s, "GET", "/api/budgets?period=2026-08", nil); json.Unmarshal(body, &august) != nil || august.Spent != 1000 {
		t.Errorf("august = %s", body)
	}
	if _, body := call(t, s, "GET", "/api/budgets", nil); json.Unmarshal(body, &september) != nil || september.Spent != 2000 || september.Period != "2026-09" {
		t.Errorf("september = %s", body)
	}
}

func TestBudgetsAPI(t *testing.T) {
	s, l := newTestServer(t, 1)
	main := mustAccount(t, l, "Main", domain.Checking, 0)
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: main.ID, Amount: 2500, CategoryID: "Groceries", Date: "2026-09-02"})

	put := func(body map[string]string) (int, domain.Budget, string) {
		t.Helper()
		code, raw := call(t, s, "PUT", "/api/budgets", body)
		var b domain.Budget
		if code == http.StatusOK {
			if err := json.Unmarshal(raw, &b); err != nil {
				t.Fatal(err)
			}
		}
		return code, b, string(raw)
	}

	code, b, raw := put(map[string]string{"category": "Groceries", "period": "2026-09", "planned": "200"})
	if code != http.StatusOK || b.Period != "2026-09" || b.Planned != 20000 || b.Spent != 2500 || len(b.Cards) != 1 {
		t.Fatalf("PUT = %d %s", code, raw)
	}
	// Blank period means the current one; a second PUT replaces the figure.
	code, b, raw = put(map[string]string{"category": "groceries", "planned": "599.99"})
	if code != http.StatusOK || b.Period != "2026-09" || b.Planned != 59999 || len(b.Cards) != 1 {
		t.Fatalf("PUT without period = %d %s", code, raw)
	}

	refused := []struct {
		name string
		body map[string]string
		code int
	}{
		{"negative", map[string]string{"category": "Groceries", "planned": "-5"}, http.StatusUnprocessableEntity},
		{"income category", map[string]string{"category": "Primary salary", "planned": "100"}, http.StatusUnprocessableEntity},
		{"unknown category", map[string]string{"category": "Nonsense", "planned": "100"}, http.StatusNotFound},
		{"no amount", map[string]string{"category": "Groceries", "planned": ""}, http.StatusUnprocessableEntity},
		{"not an amount", map[string]string{"category": "Groceries", "planned": "lots"}, http.StatusUnprocessableEntity},
		{"too fine", map[string]string{"category": "Groceries", "planned": "1.005"}, http.StatusUnprocessableEntity},
		{"bad period", map[string]string{"category": "Groceries", "period": "2026/09", "planned": "100"}, http.StatusUnprocessableEntity},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			if code, _, raw := put(c.body); code != c.code {
				t.Errorf("PUT %v = %d %s, want %d", c.body, code, raw, c.code)
			}
		})
	}

	code, raw2 := call(t, s, "GET", "/api/budgets", nil)
	if code != http.StatusOK || json.Unmarshal(raw2, &b) != nil || b.Planned != 59999 {
		t.Errorf("GET = %d %s", code, raw2)
	}
	code, raw2 = call(t, s, "GET", "/api/budgets?period=2026-10", nil)
	if code != http.StatusOK || !strings.Contains(string(raw2), `"cards":[]`) || !strings.Contains(string(raw2), `"period":"2026-10"`) {
		t.Errorf("GET october = %d %s", code, raw2)
	}
	if code, raw2 = call(t, s, "GET", "/api/budgets?period=nope", nil); code != http.StatusUnprocessableEntity {
		t.Errorf("GET bad period = %d %s", code, raw2)
	}

	// Zero removes the line.
	code, b, raw = put(map[string]string{"category": "Groceries", "planned": "0"})
	if code != http.StatusOK || len(b.Cards) != 0 || b.Planned != 0 {
		t.Errorf("PUT zero = %d %s", code, raw)
	}
}

// TestSpans is the period calendar: twelve periods back from one beginning
// on the 10th, oldest first, across a year boundary; and a key resolves to
// the period beginning on the start day of that month.
func TestSpans(t *testing.T) {
	current := span{periodStart(time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC), 10)}
	got := spansBack(current, 12)
	want := []struct{ key, label, from, to string }{
		{"2025-10", "Oct", "2025-10-10", "2025-11-09"},
		{"2025-11", "Nov", "2025-11-10", "2025-12-09"},
		{"2025-12", "Dec", "2025-12-10", "2026-01-09"},
		{"2026-01", "Jan", "2026-01-10", "2026-02-09"},
		{"2026-02", "Feb", "2026-02-10", "2026-03-09"},
		{"2026-03", "Mar", "2026-03-10", "2026-04-09"},
		{"2026-04", "Apr", "2026-04-10", "2026-05-09"},
		{"2026-05", "May", "2026-05-10", "2026-06-09"},
		{"2026-06", "Jun", "2026-06-10", "2026-07-09"},
		{"2026-07", "Jul", "2026-07-10", "2026-08-09"},
		{"2026-08", "Aug", "2026-08-10", "2026-09-09"},
		{"2026-09", "Sep", "2026-09-10", "2026-10-09"},
	}
	if len(got) != len(want) {
		t.Fatalf("spansBack = %d entries, want %d", len(got), len(want))
	}
	for i, w := range want {
		sp := got[i]
		if sp.key() != w.key || sp.label() != w.label || sp.from() != w.from || sp.to() != w.to {
			t.Errorf("spans[%d] = %s %s %s..%s, want %s %s %s..%s", i, sp.key(), sp.label(), sp.from(), sp.to(), w.key, w.label, w.from, w.to)
		}
	}

	keys := []struct {
		key      string
		startDay int
		from, to string
	}{
		{"2026-09", 1, "2026-09-01", "2026-09-30"},
		{"2026-02", 28, "2026-02-28", "2026-03-27"},
		{"2026-09", 0, "2026-09-01", "2026-09-30"},
		{"2025-12", 15, "2025-12-15", "2026-01-14"},
	}
	for _, c := range keys {
		sp, err := spanForKey(c.key, c.startDay)
		if err != nil || sp.from() != c.from || sp.to() != c.to || sp.key() != c.key {
			t.Errorf("spanForKey(%s, %d) = %s..%s %v, want %s..%s", c.key, c.startDay, sp.from(), sp.to(), err, c.from, c.to)
		}
	}
	for _, bad := range []string{"", "2026", "2026-9", "2026/09", "2026-13", "sept"} {
		if _, err := spanForKey(bad, 1); err == nil {
			t.Errorf("spanForKey(%q) accepted", bad)
		}
	}
}

// TestAddAccountCarriesEveryField pins the two fields the form used to show
// and then throw away.
func TestAddAccountCarriesEveryField(t *testing.T) {
	s, l := newTestServer(t, 1)
	code, body := call(t, s, "POST", "/api/accounts", map[string]any{
		"name": "Rainy day", "type": "savings", "currency": "EUR", "opening": "",
		"openingBalance": "250", "lowBalance": "100", "includeInNetWorth": false})
	if code != http.StatusCreated {
		t.Fatalf("add: %d %s", code, body)
	}
	var a domain.Account
	json.Unmarshal(body, &a)
	if a.LowBalance == nil || *a.LowBalance != 10000 {
		t.Fatalf("low balance %v", a.LowBalance)
	}
	if a.IncludeInNetWorth {
		t.Fatal("net worth flag was dropped")
	}
	if a.OpeningBalance != 25000 {
		t.Fatalf("opening %d", a.OpeningBalance)
	}
	got, err := l.Account(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LowBalance == nil || *got.LowBalance != 10000 || got.IncludeInNetWorth {
		t.Fatalf("%+v", got)
	}
}
