package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/feed"
)

// setBase changes the base through the API and reports the status.
func setBase(t *testing.T, s *Server, code string) (int, settingsOut) {
	t.Helper()
	status, body := call(t, s, "PUT", "/api/settings", map[string]any{"baseCurrency": code})
	var out settingsOut
	json.Unmarshal(body, &out)
	return status, out
}

func getDashboard(t *testing.T, s *Server) dashboardOut {
	t.Helper()
	status, body := call(t, s, "GET", "/api/dashboard", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/dashboard = %d %s", status, body)
	}
	var out dashboardOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestBaseCurrencyIsALens: showing the ledger in another currency scales
// every figure at the rate on file and moves no ratio, and going back
// restores every figure exactly. The rate is chosen so the arithmetic is
// exact and the test can say what each figure must be.
func TestBaseCurrencyIsALens(t *testing.T) {
	s, l := newTestServer(t, 1)
	ctx := t.Context()
	main := mustAccount(t, l, "Main", domain.Checking, 100000)
	mustAdd(t, l, domain.Transaction{Kind: domain.Income, AccountID: main.ID, Amount: 300000, CategoryID: "Primary salary", Date: "2026-09-03"})
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: main.ID, Amount: 12500, CategoryID: "Groceries", Date: "2026-09-05"})
	mustAdd(t, l, domain.Transaction{Kind: domain.Expense, AccountID: main.ID, Amount: 4000, CategoryID: "Fuel", Date: "2026-08-20"})
	if _, err := l.SetRate(ctx, "PLN", "0.25", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	if code, body := call(t, s, "PUT", "/api/budgets", map[string]string{"category": "Groceries", "planned": "200"}); code != http.StatusOK {
		t.Fatalf("PUT /api/budgets = %d %s", code, body)
	}
	fuel, _ := l.Category(ctx, "Fuel")
	if code, body := call(t, s, "PUT", "/api/categories/"+fuel.ID, map[string]any{"goalTarget": "600", "goalDue": "2026-12"}); code != http.StatusOK {
		t.Fatalf("goal: %d %s", code, body)
	}

	// Saving the settings once applies the config defaults, so the baseline
	// is taken after that rather than before.
	if code, body := call(t, s, "PUT", "/api/settings", map[string]any{"model": "limits"}); code != http.StatusOK {
		t.Fatalf("settings: %d %s", code, body)
	}
	eur := getDashboard(t, s)
	if eur.BaseCurrency != "EUR" || eur.RateReference != "EUR" || eur.Decimals["EUR"] != 2 {
		t.Fatalf("eur document: base %q reference %q decimals %v", eur.BaseCurrency, eur.RateReference, eur.Decimals)
	}
	if eur.Liquid != 383500 || eur.Totals.Expense != 12500 || eur.Totals.SavingsRate != 95 || eur.Budget.Planned != 20000 || eur.Budget.Pct != 62 {
		t.Fatalf("eur figures: liquid %d expense %d savings %d planned %d pct %d", eur.Liquid, eur.Totals.Expense, eur.Totals.SavingsRate, eur.Budget.Planned, eur.Budget.Pct)
	}

	// 1 PLN = 0.25 EUR, so every figure reads four times as large.
	status, st := setBase(t, s, "pln")
	if status != http.StatusOK || st.BaseCurrency != "PLN" || st.RateReference != "EUR" {
		t.Fatalf("setting the base: %d %+v", status, st)
	}
	pln := getDashboard(t, s)
	if pln.BaseCurrency != "PLN" || pln.RateReference != "EUR" || pln.Decimals["PLN"] != 2 {
		t.Fatalf("pln document: base %q reference %q decimals %v", pln.BaseCurrency, pln.RateReference, pln.Decimals)
	}
	four := func(name string, got, eurValue int64) {
		t.Helper()
		if got != 4*eurValue {
			t.Errorf("%s = %d in PLN, want %d", name, got, 4*eurValue)
		}
	}
	four("liquid", pln.Liquid, eur.Liquid)
	four("net worth", pln.NetWorth, eur.NetWorth)
	four("income", pln.Totals.Income, eur.Totals.Income)
	four("expense", pln.Totals.Expense, eur.Totals.Expense)
	four("net", pln.Totals.Net, eur.Totals.Net)
	four("previous expense", pln.Previous.Expense, eur.Previous.Expense)
	four("cash delta", pln.Cash.Delta, eur.Cash.Delta)
	four("last cash point", pln.Cash.Series[len(pln.Cash.Series)-1].Liquid, eur.Cash.Series[len(eur.Cash.Series)-1].Liquid)
	four("budget planned", pln.Budget.Planned, eur.Budget.Planned)
	four("budget spent", pln.Budget.Spent, eur.Budget.Spent)
	four("card planned", pln.Budget.Cards[0].Planned, eur.Budget.Cards[0].Planned)
	four("card remaining", pln.Budget.Cards[0].Remaining, eur.Budget.Cards[0].Remaining)
	four("envelopes held", pln.Envelopes.Held, eur.Envelopes.Held)
	for i := range eur.Months {
		four("month "+eur.Months[i].Key, pln.Months[i].Expense, eur.Months[i].Expense)
	}
	// Ratios are computed from reference amounts and do not move.
	if pln.Totals.SavingsRate != eur.Totals.SavingsRate || pln.Budget.Pct != eur.Budget.Pct || pln.Budget.Cards[0].Pct != eur.Budget.Cards[0].Pct {
		t.Errorf("ratios moved: savings %d/%d budget %d/%d card %d/%d", pln.Totals.SavingsRate, eur.Totals.SavingsRate, pln.Budget.Pct, eur.Budget.Pct, pln.Budget.Cards[0].Pct, eur.Budget.Cards[0].Pct)
	}
	if !reflect.DeepEqual(pln.Cash.DeltaPct, eur.Cash.DeltaPct) {
		t.Errorf("cash delta pct moved: %v to %v", *eur.Cash.DeltaPct, *pln.Cash.DeltaPct)
	}
	// Native figures stay native: an account balance is in its own currency.
	if pln.Accounts[0].Balance != eur.Accounts[0].Balance {
		t.Errorf("an account balance was converted: %d to %d", eur.Accounts[0].Balance, pln.Accounts[0].Balance)
	}

	// The goal reads in the base and is typed in it.
	_, body := call(t, s, "GET", "/api/categories", nil)
	var cats []domain.Category
	json.Unmarshal(body, &cats)
	for _, c := range cats {
		if c.ID == fuel.ID && c.GoalTarget != 240000 {
			t.Errorf("goal in PLN = %d, want 240000", c.GoalTarget)
		}
	}
	_, body = call(t, s, "PUT", "/api/categories/"+fuel.ID, map[string]any{"goalTarget": "800", "goalDue": "2026-12"})
	var edited domain.Category
	json.Unmarshal(body, &edited)
	if edited.GoalTarget != 80000 {
		t.Errorf("goal typed in PLN reads back as %d, want 80000", edited.GoalTarget)
	}
	if c, _ := l.Category(ctx, fuel.ID); c.GoalTarget != 20000 {
		t.Errorf("goal kept as %d, want 20000 in the reference", c.GoalTarget)
	}

	// A bound typed in the base filters reference amounts in the SQL, to the
	// reference's minor unit: 125.00 EUR is 500.00 PLN, so a floor at it
	// keeps the row and one at 504 PLN, which is 126 EUR, drops it.
	rows := func(min string) int {
		t.Helper()
		_, body := call(t, s, "GET", "/api/transactions?"+url.Values{"min": {min}, "kind": {"expense"}}.Encode(), nil)
		var list []domain.Transaction
		json.Unmarshal(body, &list)
		return len(list)
	}
	if rows("500") != 1 || rows("504") != 0 {
		t.Errorf("min filter in PLN kept %d and %d rows, want 1 and 0", rows("500"), rows("504"))
	}

	// The reports and the pots show in the base too.
	_, body = call(t, s, "GET", "/api/reports/spending", nil)
	var rep domain.SpendingReport
	json.Unmarshal(body, &rep)
	if rep.Total != 50000 || len(rep.Rows) == 0 || len(rep.Rows[0].Children) == 0 || rep.Rows[0].Children[0].Planned != 80000 {
		t.Errorf("spending report in PLN: total %d rows %+v", rep.Total, rep.Rows)
	}
	_, body = call(t, s, "GET", "/api/reports/metrics", nil)
	var m domain.Metrics
	json.Unmarshal(body, &m)
	if m.Expense != 50000 {
		t.Errorf("metrics expense in PLN = %d", m.Expense)
	}
	_, body = call(t, s, "GET", "/api/envelopes", nil)
	var env domain.Envelopes
	json.Unmarshal(body, &env)
	if env.Liquid != 4*eur.Liquid || env.Items[0].Assigned != 80000 {
		t.Errorf("envelopes in PLN: liquid %d assigned %d", env.Liquid, env.Items[0].Assigned)
	}

	// Back to the reference: every figure is exactly what it was.
	if status, _ := setBase(t, s, "EUR"); status != http.StatusOK {
		t.Fatalf("restoring the base: %d", status)
	}
	again := getDashboard(t, s)
	again.Insights, eur.Insights = nil, nil
	if !reflect.DeepEqual(again, eur) {
		t.Errorf("round trip moved the document:\n%+v\nwas\n%+v", again, eur)
	}
	if c, _ := l.Category(ctx, fuel.ID); c.GoalTarget != 20000 {
		t.Errorf("goal after the round trip = %d", c.GoalTarget)
	}
}

// TestBaseCurrencyIsChecked: the base is three letters and either the
// reference or a currency with a rate on file. Choosing a shipped currency
// files its starting rate on the spot, so a person is not sent to the rate
// table first; one the quote does not carry is still refused. The base
// cannot lose its last rate.
func TestBaseCurrencyIsChecked(t *testing.T) {
	s, l := newTestServer(t, 1)
	for _, code := range []string{"eu", "euro", "pl1", "BTC", ""} {
		if status, _ := setBase(t, s, code); status != http.StatusUnprocessableEntity {
			t.Errorf("base %q: %d, want 422", code, status)
		}
	}
	if status, st := setBase(t, s, "usd"); status != http.StatusOK || st.BaseCurrency != "USD" {
		t.Fatalf("base USD: %d %+v", status, st)
	}
	_, body := call(t, s, "GET", "/api/rates?currency=USD", nil)
	var rates []domain.FXRate
	json.Unmarshal(body, &rates)
	if len(rates) != 1 || rates[0].Date != feed.Builtin.Date || rates[0].Rate != "0.87260035" || rates[0].Source != domain.SourceSeed {
		t.Fatalf("starting rate: %+v", rates)
	}
	if status, _ := call(t, s, "DELETE", "/api/rates/USD/"+feed.Builtin.Date, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("the base's last rate went: %d", status)
	}
	if _, err := l.SetRate(t.Context(), "USD", "0.9", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	if status, _ := call(t, s, "DELETE", "/api/rates/USD/"+feed.Builtin.Date, nil); status != http.StatusOK {
		t.Errorf("a rate of the base with another on file stayed: %d", status)
	}
	if _, err := l.SetRate(t.Context(), "USD", "0.91", "2026-06-01"); err != nil {
		t.Fatal(err)
	}

	// The large amount is filed in the base it was typed in and shown in
	// whatever the base is now.
	if code, body := call(t, s, "PUT", "/api/settings", map[string]any{"largeAmount": "910"}); code != http.StatusOK {
		t.Fatalf("large amount: %d %s", code, body)
	}
	if cfg := s.Config(); cfg.LargeAmount != 91000 || cfg.LargeAmountCurrency != "USD" {
		t.Fatalf("large amount kept as %d %s", cfg.LargeAmount, cfg.LargeAmountCurrency)
	}
	if status, st := setBase(t, s, "EUR"); status != http.StatusOK || st.LargeAmount != 82810 {
		t.Errorf("large amount shown in EUR = %d (status %d), want 82810", st.LargeAmount, status)
	}
	if cfg := s.Config(); cfg.LargeAmount != 91000 || cfg.LargeAmountCurrency != "USD" {
		t.Errorf("changing the base rewrote the large amount to %d %s", cfg.LargeAmount, cfg.LargeAmountCurrency)
	}
}
