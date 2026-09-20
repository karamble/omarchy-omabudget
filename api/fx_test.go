package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/karamble/omarchy-omabudget/config"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/feed"
	"github.com/karamble/omarchy-omabudget/mcpserver"
	"github.com/karamble/omarchy-omabudget/money"
)

// today is the real clock's date: the ledger judges a quote's age by its
// own clock, which a test in this package cannot move, so a fake quote is
// dated from the same clock or it goes stale in a year.
func today() string { return time.Now().UTC().Format("2006-01-02") }

// fakeFeed stands in for the network: it counts every call, keeps the last
// source asked for, and answers with a quote or an error.
type fakeFeed struct {
	calls atomic.Int32
	last  feed.Source
	quote feed.Quote
	err   error
}

func (f *fakeFeed) fetch(ctx context.Context, src feed.Source) (feed.Quote, error) {
	f.calls.Add(1)
	f.last = src
	if _, ok := ctx.Deadline(); !ok {
		return feed.Quote{}, context.DeadlineExceeded
	}
	return f.quote, f.err
}

func fakeQuote() feed.Quote {
	return feed.Quote{Base: "EUR", Date: today(), Rates: map[string]money.Rate{
		"USD": "1.1", "GBP": "0.9", "PLN": "4.4",
	}}
}

// route is one registration in the server's mux.
var route = regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+ )?([^"]+)"`)

// TestNothingFetchesOnItsOwn drives every route the server registers, the
// whole MCP surface and the alert snapshot with the fetch replaced by a
// counter, and requires it to read zero. The routes are read from
// server.go, so a new one fails here until it is driven too. Then the one
// route that may fetch is pressed, and the counter reads one; accepting a
// held rate leaves it there.
func TestNothingFetchesOnItsOwn(t *testing.T) {
	s, l := newTestServer(t, 1)
	fake := &fakeFeed{quote: fakeQuote()}
	s.fetch = fake.fetch
	ctx := context.Background()

	main := mustAccount(t, l, "Main", domain.Checking, 100000)
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: main.ID, Amount: 2500, CategoryID: "Groceries", Date: "2026-09-02"})

	// Every route, with a body where one is read. A refused request still
	// runs the handler up to the refusal, which is all that matters here.
	type req struct {
		path string
		body any
	}
	drive := map[string][]req{
		"GET /api/health":                     {{"/api/health", nil}},
		"GET /api/dashboard":                  {{"/api/dashboard", nil}},
		"GET /api/accounts":                   {{"/api/accounts", nil}},
		"POST /api/accounts":                  {{"/api/accounts", map[string]any{"name": "Dollars", "type": "checking", "currency": "USD", "openingBalance": "500", "openingDate": "2026-01-01"}}},
		"PUT /api/accounts/{id}":              {{"/api/accounts/" + main.ID, map[string]any{"name": "Main", "currency": "GBP"}}},
		"DELETE /api/accounts/{id}":           {{"/api/accounts/nope", nil}},
		"GET /api/reconcile":                  {{"/api/reconcile?account=Main&statement=1000", nil}},
		"POST /api/reconcile":                 {{"/api/reconcile", map[string]any{"account": "Main", "statement": "1000"}}},
		"GET /api/categories":                 {{"/api/categories", nil}},
		"GET /api/budgets":                    {{"/api/budgets", nil}},
		"PUT /api/budgets":                    {{"/api/budgets", map[string]string{"category": "Groceries", "planned": "200"}}},
		"POST /api/budgets/helpers":           {{"/api/budgets/helpers", map[string]any{"action": "copy", "period": "2026-10"}}},
		"POST /api/budgets/rollover":          {{"/api/budgets/rollover", map[string]any{"from": "2026-08", "to": "2026-09"}}},
		"GET /api/envelopes":                  {{"/api/envelopes", nil}},
		"POST /api/categories":                {{"/api/categories", map[string]any{"name": "Plants"}}},
		"PUT /api/categories/{id...}":         {{"/api/categories/nope", map[string]any{"name": "Nope"}}},
		"DELETE /api/categories/{id...}":      {{"/api/categories/nope", nil}},
		"GET /api/settings":                   {{"/api/settings", nil}},
		"PUT /api/settings":                   {{"/api/settings", map[string]any{"baseCurrency": "USD"}}, {"/api/settings", map[string]any{"rateSource": "frankfurter", "rateSourceUrl": "http://127.0.0.1:9/v1/latest"}}, {"/api/settings", map[string]any{"rateSource": "ecb"}}},
		"GET /api/payees":                     {{"/api/payees", nil}},
		"PUT /api/payees/{id}":                {{"/api/payees/nope", map[string]any{"name": "Nope"}}},
		"DELETE /api/payees/{id}":             {{"/api/payees/nope", nil}},
		"GET /api/rates":                      {{"/api/rates", nil}},
		"PUT /api/rates":                      {{"/api/rates", map[string]string{"currency": "PLN", "rate": "0.25"}}},
		"DELETE /api/rates/{currency}/{date}": {{"/api/rates/PLN/2020-01-01", nil}},
		"POST /api/rates/accept":              {{"/api/rates/accept", map[string]string{"currency": "GBP", "rate": "1.2", "date": today(), "source": "ecb"}}},
		"GET /api/rates/sources":              {{"/api/rates/sources", nil}},
		"GET /api/reports/spending":           {{"/api/reports/spending", nil}},
		"GET /api/reports/metrics":            {{"/api/reports/metrics", nil}},
		"POST /api/export":                    {{"/api/export", map[string]any{"path": "/nowhere/omabudget.journal"}}},
		"POST /api/backup":                    {{"/api/backup", map[string]any{"path": "/nowhere/omabudget.db"}}},
		"GET /api/rules":                      {{"/api/rules", nil}},
		"POST /api/rules":                     {{"/api/rules", map[string]any{"name": "Rent", "amount": "10", "category": "Rent", "frequency": "monthly", "startDate": "2026-09-03"}}},
		"PUT /api/rules/{id}":                 {{"/api/rules/nope", map[string]any{"name": "Nope"}}},
		"DELETE /api/rules/{id}":              {{"/api/rules/nope", nil}},
		"POST /api/rules/{id}/post":           {{"/api/rules/nope/post", map[string]any{}}},
		"POST /api/rules/{id}/skip":           {{"/api/rules/nope/skip", map[string]any{}}},
		"GET /api/bills":                      {{"/api/bills", nil}},
		"GET /api/transactions":               {{"/api/transactions", nil}},
		"POST /api/transactions":              {{"/api/transactions", map[string]any{"account": "Dollars", "amount": "12", "category": "Groceries", "date": "2026-09-05"}}, {"/api/transactions", map[string]any{"account": "Main", "amount": "12", "currency": "GBP", "category": "Groceries", "date": "2026-09-05"}}},
		"GET /api/transactions/{id}":          {{"/api/transactions/nope", nil}},
		"PUT /api/transactions/{id}":          {{"/api/transactions/nope", map[string]any{"amount": "13"}}},
		"DELETE /api/transactions/{id}":       {{"/api/transactions/nope", nil}},
		"POST /api/transactions/{id}/restore": {{"/api/transactions/nope/restore", nil}},
		"GET /api/catalogue":                  {{"/api/catalogue", nil}},
		"GET /api/alerts":                     {{"/api/alerts", nil}},
		"POST /api/alerts":                    {{"/api/alerts", map[string]any{"path": "nope"}}},
		"PUT /api/alerts/{id}":                {{"/api/alerts/nope", map[string]any{}}},
		"DELETE /api/alerts/{id}":             {{"/api/alerts/nope", nil}},
		"POST /api/monitoring":                {{"/api/monitoring", map[string]any{"enabled": true}}},
		"POST /api/mcp":                       {{"/api/mcp", map[string]any{"enabled": true}}},
		"POST /api/token/recycle":             {{"/api/token/recycle", nil}},
		"/mcp":                                {{"/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}}},
	}
	// The order matters where one request sets up another: the dollar
	// account before the entry in it, the base after the USD rate exists.
	order := []string{
		"POST /api/accounts", "POST /api/transactions", "PUT /api/accounts/{id}", "PUT /api/settings", "PUT /api/rates", "POST /api/rates/accept",
	}
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range route.FindAllStringSubmatch(string(src), -1) {
		pattern := m[1] + m[2]
		seen[pattern] = true
		if pattern == "POST /api/rates/fetch" {
			continue
		}
		if _, ok := drive[pattern]; !ok {
			t.Errorf("%s is registered but not driven here: add it to the list", pattern)
		}
	}
	for pattern := range drive {
		if !seen[pattern] {
			t.Errorf("%s is driven here but not registered", pattern)
		}
	}
	// Recycling the token locks every later call out, so it goes last and
	// the token is put back; a 401 anywhere means a handler was not reached.
	const recycle = "POST /api/token/recycle"
	done := map[string]bool{recycle: true}
	hit := func(pattern string) {
		method, _, _ := strings.Cut(pattern, " ")
		if method == pattern {
			method = http.MethodPost
		}
		for _, r := range drive[pattern] {
			if code, body := call(t, s, method, r.path, r.body); code == http.StatusUnauthorized {
				t.Errorf("%s %s was not reached: %s", method, r.path, body)
			}
		}
		done[pattern] = true
	}
	for _, pattern := range order {
		hit(pattern)
	}
	rest := make([]string, 0, len(drive))
	for pattern := range drive {
		if !done[pattern] {
			rest = append(rest, pattern)
		}
	}
	sort.Strings(rest)
	for _, pattern := range rest {
		hit(pattern)
	}
	done[recycle] = false
	hit(recycle)
	if err := s.mutate(func(c *config.Config) error { c.APIToken = "test-token"; return nil }); err != nil {
		t.Fatal(err)
	}
	// The base is USD after the settings run, so both a foreign entry and a
	// base that is not the reference have been shown.
	if base := s.Config().BaseCurrency; base != "USD" {
		t.Errorf("base = %s after the settings run, want USD", base)
	}

	// The MCP surface, called as the server hands it to the MCP handler.
	if _, err := s.mcpDashboard(ctx); err != nil {
		t.Error(err)
	}
	if _, err := s.mcpTransactions(ctx, mcpserver.TransactionQuery{}); err != nil {
		t.Error(err)
	}
	if _, err := s.mcpAddTransaction(ctx, map[string]any{"account": "Dollars", "amount": "7", "category": "Groceries", "date": "2026-09-06"}); err != nil {
		t.Error(err)
	}
	if _, err := s.mcpBudget(ctx, ""); err != nil {
		t.Error(err)
	}
	if _, err := s.mcpBills(ctx, 30); err != nil {
		t.Error(err)
	}
	if _, err := s.mcpSpending(ctx, ""); err != nil {
		t.Error(err)
	}
	s.Snapshot()

	if n := fake.calls.Load(); n != 0 {
		t.Fatalf("the fetch ran %d times without being asked", n)
	}
	if _, ok, err := l.LastFetch(ctx); err != nil || ok {
		t.Fatalf("a fetch was recorded without running: ok=%v err=%v", ok, err)
	}

	// The one route that may: the source in settings is the bank again, and
	// the currencies in use are the dollar and pound accounts, the pound
	// entry and the base.
	code, body := call(t, s, "POST", "/api/rates/fetch", nil)
	if code != http.StatusOK {
		t.Fatalf("POST /api/rates/fetch = %d %s", code, body)
	}
	if n := fake.calls.Load(); n != 1 {
		t.Fatalf("the fetch ran %d times for one press", n)
	}
	var out fetchOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Source != "ecb" || out.Host != "www.ecb.europa.eu" || out.Published != today() {
		t.Errorf("fetch = %s %s %s", out.Source, out.Host, out.Published)
	}
	if strings.Join(out.Filed, ",") != "GBP,USD" {
		t.Errorf("filed %v, want GBP and USD", out.Filed)
	}
	if !out.Rates["USD"].Equal("0.90909091") || len(out.Rates) != 2 {
		t.Errorf("rates %v", out.Rates)
	}
	if fake.last.ID != "ecb" || fake.last.URL != feed.ECB.URL {
		t.Errorf("asked %s at %s", fake.last.ID, fake.last.URL)
	}

	code, body = call(t, s, "POST", "/api/rates/accept", map[string]string{"currency": "CHF", "rate": "1.05", "date": today(), "source": "ecb"})
	if code != http.StatusOK {
		t.Fatalf("POST /api/rates/accept = %d %s", code, body)
	}
	if n := fake.calls.Load(); n != 1 {
		t.Fatalf("accepting a rate ran the fetch: %d calls", n)
	}
	f, ok, err := l.LastFetch(ctx)
	if err != nil || !ok || f.Source != "ecb" || f.Host != "www.ecb.europa.eu" {
		t.Errorf("last fetch = %+v ok=%v err=%v", f, ok, err)
	}
}

// TestFetchIsRecordedBeforeItIsAnswered: a fetch that fails still leaves
// the record, since the connection was made; the answer names the host and
// not the source's body; and a second press on the same day changes
// nothing.
func TestFetchIsRecordedBeforeItIsAnswered(t *testing.T) {
	s, l := newTestServer(t, 1)
	fake := &fakeFeed{err: context.DeadlineExceeded}
	s.fetch = fake.fetch
	ctx := context.Background()
	if _, err := l.AddAccount(ctx, domain.Account{Name: "Dollars", Type: domain.Checking, Currency: "USD", OpeningBalance: 100, OpeningDate: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}

	code, body := call(t, s, "POST", "/api/rates/fetch", nil)
	if code != http.StatusBadGateway {
		t.Fatalf("a failed fetch answered %d %s", code, body)
	}
	if !strings.Contains(string(body), "www.ecb.europa.eu") {
		t.Errorf("the error does not name the host: %s", body)
	}
	f, ok, err := l.LastFetch(ctx)
	if err != nil || !ok || f.Host != "www.ecb.europa.eu" {
		t.Errorf("a failed fetch left no record: %+v ok=%v err=%v", f, ok, err)
	}
	code, body = call(t, s, "GET", "/api/settings", nil)
	var settings settingsOut
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK || settings.LastFetch == nil || settings.LastFetch.Host != "www.ecb.europa.eu" || settings.RateSource != "ecb" {
		t.Errorf("settings after a failed fetch: %d %s", code, body)
	}

	fake.err, fake.quote = nil, fakeQuote()
	code, body = call(t, s, "POST", "/api/rates/fetch", nil)
	var first, second fetchOut
	if err := json.Unmarshal(body, &first); err != nil || code != http.StatusOK {
		t.Fatalf("POST /api/rates/fetch = %d %s", code, body)
	}
	code, body = call(t, s, "POST", "/api/rates/fetch", nil)
	if err := json.Unmarshal(body, &second); err != nil || code != http.StatusOK {
		t.Fatalf("second press = %d %s", code, body)
	}
	if strings.Join(first.Filed, ",") != "USD" || strings.Join(second.Unchanged, ",") != "USD" || len(second.Filed) != 0 {
		t.Errorf("first filed %v, second filed %v unchanged %v", first.Filed, second.Filed, second.Unchanged)
	}
	if n := fake.calls.Load(); n != 3 {
		t.Errorf("%d calls for three presses", n)
	}
}

// TestRateSourceIsCheckedWhenChosen: the source setting is refused at save
// time with the message a fetch would give, a URL goes only with a source
// that allows one, choosing a source drops the last one's URL, an empty
// URL is the source's own, and a press can override the setting without
// changing it.
func TestRateSourceIsCheckedWhenChosen(t *testing.T) {
	s, _ := newTestServer(t, 1)
	fake := &fakeFeed{quote: fakeQuote()}
	s.fetch = fake.fetch

	refused := []map[string]any{
		{"rateSource": "nope"},
		{"rateSource": "ecb", "rateSourceUrl": "https://example.invalid/latest"},
		{"rateSource": "frankfurter", "rateSourceUrl": "http://example.invalid/latest"},
		{"rateSource": "frankfurter", "rateSourceUrl": "latest"},
		{"rateSource": "frankfurter", "rateSourceUrl": "ftp://127.0.0.1/latest"},
	}
	for _, in := range refused {
		if code, body := call(t, s, "PUT", "/api/settings", in); code != http.StatusUnprocessableEntity {
			t.Errorf("%v was accepted: %d %s", in, code, body)
		}
	}
	code, body := call(t, s, "PUT", "/api/settings", map[string]any{"rateSource": "frankfurter", "rateSourceUrl": "http://example.invalid/latest"})
	var e struct {
		Error string `json:"error"`
	}
	json.Unmarshal(body, &e)
	if want := feed.CheckURL("http://example.invalid/latest").Error(); code != http.StatusUnprocessableEntity || e.Error != want {
		t.Errorf("refused with %q, want the fetch's own %q", e.Error, want)
	}
	if c := s.Config(); c.RateSource != "" || c.RateSourceURL != "" {
		t.Errorf("a refused choice was saved: %q %q", c.RateSource, c.RateSourceURL)
	}

	own := "http://192.168.1.20:8080/v1/latest"
	code, out := setSource(t, s, map[string]any{"rateSource": "frankfurter", "rateSourceUrl": own})
	if code != http.StatusOK || out.RateSource != "frankfurter" || out.RateSourceURL != own {
		t.Fatalf("choosing an instance of one's own: %d %+v", code, out)
	}
	if code, body := call(t, s, "POST", "/api/rates/fetch", nil); code != http.StatusOK {
		t.Fatalf("POST /api/rates/fetch = %d %s", code, body)
	}
	if fake.last.ID != "frankfurter" || fake.last.URL != own {
		t.Errorf("fetched %s at %s, want frankfurter at %s", fake.last.ID, fake.last.URL, own)
	}
	if f, ok, _ := s.Ledger().LastFetch(context.Background()); !ok || f.Host != "192.168.1.20:8080" || f.Source != "frankfurter" {
		t.Errorf("recorded %+v", f)
	}

	// One press elsewhere leaves the setting alone.
	if code, body := call(t, s, "POST", "/api/rates/fetch", map[string]string{"source": "ecb"}); code != http.StatusOK {
		t.Fatalf("override press = %d %s", code, body)
	}
	if fake.last.ID != "ecb" || fake.last.URL != feed.ECB.URL {
		t.Errorf("override fetched %s at %s", fake.last.ID, fake.last.URL)
	}
	if code, body := call(t, s, "POST", "/api/rates/fetch", map[string]string{"source": "ecb", "url": own}); code != http.StatusUnprocessableEntity {
		t.Errorf("a url on the bank was accepted for one press: %d %s", code, body)
	}
	if c := s.Config(); c.RateSource != "frankfurter" || c.RateSourceURL != own {
		t.Errorf("a press changed the setting: %q %q", c.RateSource, c.RateSourceURL)
	}

	code, out = setSource(t, s, map[string]any{"rateSourceUrl": ""})
	if code != http.StatusOK || out.RateSource != "frankfurter" || out.RateSourceURL != "" {
		t.Errorf("an empty url did not reset: %d %+v", code, out)
	}
	setSource(t, s, map[string]any{"rateSource": "frankfurter", "rateSourceUrl": own})
	code, out = setSource(t, s, map[string]any{"rateSource": "ecb"})
	if code != http.StatusOK || out.RateSource != "ecb" || out.RateSourceURL != "" {
		t.Errorf("choosing the bank kept the other's url: %d %+v", code, out)
	}
	if fake.calls.Load() != 2 {
		t.Errorf("%d fetches for two presses", fake.calls.Load())
	}
}

func setSource(t *testing.T, s *Server, in map[string]any) (int, settingsOut) {
	t.Helper()
	code, body := call(t, s, "PUT", "/api/settings", in)
	var out settingsOut
	json.Unmarshal(body, &out)
	return code, out
}

// TestRateSourcesListsTheRegistry: the picker reads what the registry
// holds, in its order, with who answers.
func TestRateSourcesListsTheRegistry(t *testing.T) {
	s, _ := newTestServer(t, 1)
	code, body := call(t, s, "GET", "/api/rates/sources", nil)
	var out []sourceOut
	if err := json.Unmarshal(body, &out); err != nil || code != http.StatusOK {
		t.Fatalf("GET /api/rates/sources = %d %s", code, body)
	}
	if len(out) != len(feed.Sources) {
		t.Fatalf("%d sources, want %d", len(out), len(feed.Sources))
	}
	for i, src := range feed.Sources {
		if out[i].ID != src.ID || out[i].Name != src.Name || out[i].What != src.What || out[i].URL != src.URL || out[i].Custom != src.Custom {
			t.Errorf("source %d = %+v", i, out[i])
		}
	}
}
