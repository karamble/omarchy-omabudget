package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/karamble/omarchy-omabudget/client"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/money"
)

// reconcile holds a statement against the ledger. A line's place on a
// statement is its status, so ticking one is an ordinary edit:
//
//	omabudget reconcile Checking 1842.19
//	omabudget reconcile Checking 1842.19 -through 2026-08-31
//	omabudget edit <id> -status pending          # it is not on the statement
//	omabudget reconcile Checking 1842.19 -finish
//
// A transfer is one row with one status, so settling it from either account
// settles it for both.
func runReconcile(args []string) error {
	fs := flag.NewFlagSet("reconcile", flag.ExitOnError)
	c := bind(fs)
	through := fs.String("through", "", "the statement's closing date, YYYY-MM-DD (default: today)")
	finish := fs.Bool("finish", false, "settle the sheet: every ticked line becomes reconciled")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return errors.New("usage: omabudget reconcile <account> <closing balance> [-through YYYY-MM-DD] [-finish]")
	}
	statement := pos[len(pos)-1]
	account := strings.Join(pos[:len(pos)-1], " ")

	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()

	if *finish {
		var out struct {
			Sheet      domain.Reconciliation `json:"sheet"`
			Reconciled int                   `json:"reconciled"`
		}
		body := map[string]string{"account": account, "through": *through, "statement": statement}
		if err := cl.Do(ctx, "POST", "/api/reconcile", body, &out); err != nil {
			return err
		}
		if c.json {
			return client.PrintJSON(out)
		}
		fmt.Printf("%s settled to %s at %s: %d %s reconciled\n", out.Sheet.Account,
			money.New(out.Sheet.Settled, out.Sheet.Currency).Format(), out.Sheet.Through,
			out.Reconciled, pick(out.Reconciled == 1, "line", "lines"))
		return nil
	}

	q := url.Values{"account": {account}, "statement": {statement}}
	if *through != "" {
		q.Set("through", *through)
	}
	var sheet domain.Reconciliation
	if err := cl.Do(ctx, "GET", "/api/reconcile?"+q.Encode(), nil, &sheet); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(sheet)
	}
	printSheet(sheet)
	return nil
}

func pick(one bool, a, b string) string {
	if one {
		return a
	}
	return b
}

func printSheet(sheet domain.Reconciliation) {
	amount := func(minor int64) string { return money.New(minor, sheet.Currency).Format() }
	fmt.Printf("%s to %s\n", sheet.Account, sheet.Through)
	fmt.Printf("  statement   %s\n", amount(sheet.Statement))
	fmt.Printf("  settled     %s\n", amount(sheet.Settled))
	fmt.Printf("  ticked      %s\n", amount(sheet.Ticked))
	switch {
	case sheet.Difference == 0 && sheet.Ticked == 0 && len(sheet.Rows) == 0:
		fmt.Printf("  difference  %s   nothing is waiting: this statement is settled\n", amount(0))
	case sheet.Difference == 0:
		fmt.Printf("  difference  %s   it adds up: omabudget reconcile %q %s -finish\n",
			amount(0), sheet.Account, amount(sheet.Statement))
	default:
		fmt.Printf("  difference  %s   tick what is on the statement, or add what is missing\n",
			amount(sheet.Difference))
	}
	if len(sheet.Rows) == 0 {
		return
	}
	fmt.Println()
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "ON\tDATE\tAMOUNT\tDESCRIPTION\tID\n")
	for _, t := range sheet.Rows {
		mark := " "
		if t.Status == domain.Cleared {
			mark = "x"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", mark, t.Date,
			money.New(t.Signed, sheet.Currency).Format(), t.Description, t.ID)
	}
	tw.Flush()
}
