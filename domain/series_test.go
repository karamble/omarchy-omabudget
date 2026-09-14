package domain

import (
	"context"
	"math"
	"reflect"
	"testing"
)

// addOn adds a transaction, failing the test on error.
func addOn(t *testing.T, l *Ledger, tx Transaction) Transaction {
	t.Helper()
	out, err := l.Add(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTotals is the statistics rule: transfers, excluded categories and
// deleted rows stay out, splits count in full, and the savings rate is a
// whole percentage truncated toward zero, 0 without income.
func TestTotals(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	save := mustAccount(t, l, "Savings", Savings, "EUR", 0)

	addOn(t, l, Transaction{Kind: Income, AccountID: main.ID, Amount: 300000, CategoryID: "Primary salary", Date: "2026-09-03"})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 2500, CategoryID: "Groceries", Date: "2026-09-02"})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 10000, Date: "2026-09-07", Splits: []Split{
		{CategoryID: "food/groceries", Amount: 6000},
		{CategoryID: "food/household-chemicals-hygiene", Amount: 4000},
	}})
	addOn(t, l, Transaction{Kind: Transfer, AccountID: main.ID, CounterAccountID: save.ID, Amount: 20000, Date: "2026-09-06"})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 5000, CategoryID: "Emergency fund", Date: "2026-09-08"})
	gone := addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 99900, CategoryID: "Groceries", Date: "2026-09-09"})
	if err := l.SoftDelete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	// August: spending only. July: more out than in. June: 66.67 percent.
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 1000, CategoryID: "Fuel", Date: "2026-08-20"})
	addOn(t, l, Transaction{Kind: Income, AccountID: main.ID, Amount: 1000, CategoryID: "Bonus", Date: "2026-07-05"})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 1500, CategoryID: "Fuel", Date: "2026-07-06"})
	addOn(t, l, Transaction{Kind: Income, AccountID: main.ID, Amount: 3000, CategoryID: "Bonus", Date: "2026-06-05"})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 1000, CategoryID: "Fuel", Date: "2026-06-06"})

	cases := []struct {
		name     string
		from, to string
		want     Totals
	}{
		{"september", "2026-09-01", "2026-09-30", Totals{Income: 300000, Expense: 12500, Net: 287500, SavingsRate: 95}},
		{"no income", "2026-08-01", "2026-08-31", Totals{Income: 0, Expense: 1000, Net: -1000, SavingsRate: 0}},
		{"negative rate", "2026-07-01", "2026-07-31", Totals{Income: 1000, Expense: 1500, Net: -500, SavingsRate: -50}},
		{"truncated rate", "2026-06-01", "2026-06-30", Totals{Income: 3000, Expense: 1000, Net: 2000, SavingsRate: 66}},
		{"empty", "2026-01-01", "2026-01-31", Totals{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := l.Totals(ctx, c.from, c.to)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Totals(%s, %s) = %+v, want %+v", c.from, c.to, got, c.want)
			}
		})
	}
	if _, err := l.Totals(ctx, "2026-09-30", "2026-09-01"); err == nil {
		t.Error("reversed range accepted")
	}
	if _, err := l.Totals(ctx, "2026-9-1", "2026-09-30"); err == nil {
		t.Error("malformed date accepted")
	}
}

// TestLiquidSeries walks a period day by day: opening balances count from
// their opening date, a transfer between two liquid accounts is a wash, a
// transfer out to a non-liquid or foreign account is not, deleted rows and
// foreign-currency accounts never enter, and the last point is what Balance
// says the liquid accounts hold less anything dated after the span.
func TestLiquidSeries(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	main := mustAccount(t, l, "Main", Checking, "EUR", 100000)
	broker := mustAccount(t, l, "Broker", Investment, "EUR", 200000)
	wallet := mustAccount(t, l, "Wallet", Cash, "PLN", 10000)
	save, err := l.AddAccount(ctx, Account{Name: "Savings", Type: Savings, Currency: "EUR", OpeningBalance: 50000, OpeningDate: "2026-09-05"})
	if err != nil {
		t.Fatal(err)
	}

	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 1000, CategoryID: "Fuel", Date: "2026-08-20"})
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 2500, CategoryID: "Groceries", Date: "2026-09-02"})
	addOn(t, l, Transaction{Kind: Income, AccountID: main.ID, Amount: 300000, CategoryID: "Primary salary", Date: "2026-09-03"})
	addOn(t, l, Transaction{Kind: Transfer, AccountID: main.ID, CounterAccountID: save.ID, Amount: 20000, Date: "2026-09-06"})
	addOn(t, l, Transaction{Kind: Transfer, AccountID: main.ID, CounterAccountID: broker.ID, Amount: 30000, Date: "2026-09-08"})
	gone := addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 99900, CategoryID: "Groceries", Date: "2026-09-09"})
	if err := l.SoftDelete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	addOn(t, l, Transaction{Kind: Expense, AccountID: wallet.ID, Amount: 1000, CategoryID: "Fuel", Date: "2026-09-10", FXRate: "0.25"})
	received := int64(43125)
	addOn(t, l, Transaction{Kind: Transfer, AccountID: main.ID, CounterAccountID: wallet.ID, Amount: 10000, CounterAmount: &received, Date: "2026-09-11"})
	// Dated after the span: rent entered ahead and an account opening next month.
	addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 5000, CategoryID: "Rent", Date: "2026-09-20"})
	later, err := l.AddAccount(ctx, Account{Name: "Later", Type: Savings, Currency: "EUR", OpeningBalance: 500, OpeningDate: "2026-10-01"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := l.LiquidSeries(ctx, "2026-09-01", "2026-09-13")
	if err != nil {
		t.Fatal(err)
	}
	want := []CashPoint{
		{"2026-09-01", 99000}, {"2026-09-02", 96500}, {"2026-09-03", 396500}, {"2026-09-04", 396500},
		{"2026-09-05", 446500}, {"2026-09-06", 446500}, {"2026-09-07", 446500}, {"2026-09-08", 416500},
		{"2026-09-09", 416500}, {"2026-09-10", 416500}, {"2026-09-11", 406500}, {"2026-09-12", 406500},
		{"2026-09-13", 406500},
	}
	if !reflect.DeepEqual(got.Series, want) {
		t.Errorf("series = %v, want %v", got.Series, want)
	}
	if got.Delta != 307500 {
		t.Errorf("delta = %d, want 307500", got.Delta)
	}
	if got.DeltaPct == nil || *got.DeltaPct != 310.6 {
		t.Errorf("deltaPct = %v, want 310.6", got.DeltaPct)
	}
	// Balance is undated: it already holds the rent and the later opening.
	last := got.Series[len(got.Series)-1].Liquid
	if sum := balance(t, l, main.ID) + balance(t, l, save.ID) + balance(t, l, later.ID); last != sum+5000-500 {
		t.Errorf("last point %d, liquid balances sum to %d, want them apart by the rows dated after the span", last, sum)
	}
}

func TestLiquidSeriesEdges(t *testing.T) {
	ctx := context.Background()
	t.Run("no liquid accounts", func(t *testing.T) {
		l := newLedger(t)
		mustAccount(t, l, "Broker", Investment, "EUR", 200000)
		got, err := l.LiquidSeries(ctx, "2026-09-01", "2026-09-03")
		if err != nil {
			t.Fatal(err)
		}
		want := []CashPoint{{"2026-09-01", 0}, {"2026-09-02", 0}, {"2026-09-03", 0}}
		if !reflect.DeepEqual(got.Series, want) || got.Delta != 0 || got.DeltaPct != nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("nothing before the period", func(t *testing.T) {
		l := newLedger(t)
		main, err := l.AddAccount(ctx, Account{Name: "Main", Type: Checking, Currency: "EUR", OpeningDate: "2026-09-01"})
		if err != nil {
			t.Fatal(err)
		}
		addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 100, CategoryID: "Fuel", Date: "2026-09-02"})
		got, err := l.LiquidSeries(ctx, "2026-09-01", "2026-09-02")
		if err != nil {
			t.Fatal(err)
		}
		if got.Delta != -100 || got.DeltaPct != nil {
			t.Errorf("delta %d pct %v, want -100 and no percentage", got.Delta, got.DeltaPct)
		}
	})
	t.Run("drop that rounds to zero", func(t *testing.T) {
		l := newLedger(t)
		main := mustAccount(t, l, "Main", Checking, "EUR", 100000)
		addOn(t, l, Transaction{Kind: Expense, AccountID: main.ID, Amount: 1, CategoryID: "Fuel", Date: "2026-09-02"})
		got, err := l.LiquidSeries(ctx, "2026-09-01", "2026-09-13")
		if err != nil {
			t.Fatal(err)
		}
		if got.Delta != -1 || got.DeltaPct == nil || *got.DeltaPct != 0 || math.Signbit(*got.DeltaPct) {
			t.Errorf("delta %d pct %v, want -1 and a plain 0", got.Delta, got.DeltaPct)
		}
	})
	t.Run("overdrawn at the start", func(t *testing.T) {
		l := newLedger(t)
		main := mustAccount(t, l, "Main", Checking, "EUR", -100000)
		addOn(t, l, Transaction{Kind: Income, AccountID: main.ID, Amount: 300000, CategoryID: "Bonus", Date: "2026-09-03"})
		got, err := l.LiquidSeries(ctx, "2026-09-01", "2026-09-13")
		if err != nil {
			t.Fatal(err)
		}
		if got.Delta != 300000 || got.DeltaPct == nil || *got.DeltaPct != 300 {
			t.Errorf("delta %d pct %v, want 300000 and 300", got.Delta, got.DeltaPct)
		}
	})
	t.Run("opened after the period", func(t *testing.T) {
		l := newLedger(t)
		if _, err := l.AddAccount(ctx, Account{Name: "Main", Type: Checking, Currency: "EUR", OpeningBalance: 500, OpeningDate: "2026-10-01"}); err != nil {
			t.Fatal(err)
		}
		got, err := l.LiquidSeries(ctx, "2026-09-12", "2026-09-13")
		if err != nil {
			t.Fatal(err)
		}
		want := []CashPoint{{"2026-09-12", 0}, {"2026-09-13", 0}}
		if !reflect.DeepEqual(got.Series, want) {
			t.Errorf("series = %v, want %v", got.Series, want)
		}
	})
	t.Run("bad range", func(t *testing.T) {
		l := newLedger(t)
		if _, err := l.LiquidSeries(ctx, "2026-09-13", "2026-09-12"); err == nil {
			t.Error("reversed range accepted")
		}
		if _, err := l.LiquidSeries(ctx, "today", "2026-09-12"); err == nil {
			t.Error("malformed date accepted")
		}
	})
}
