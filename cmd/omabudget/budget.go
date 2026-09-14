package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/karamble/omarchy-omabudget/client"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/money"
)

// runBudget shows the period's plan against what was spent, or files one
// line of it. The amount is text like quick-add's; 0 removes the line.
//
//	omabudget budget
//	omabudget budget -period 2026-08
//	omabudget budget set Groceries 600
//	omabudget budget set "Coffee & snacks" 0 -period 2026-10
//	omabudget budget copy -period 2026-10
//	omabudget budget average 6 Groceries
//	omabudget budget median
//	omabudget budget scale 5
func runBudget(args []string) error {
	fs := flag.NewFlagSet("budget", flag.ExitOnError)
	c := bind(fs)
	period := fs.String("period", "", "YYYY-MM (default: the current period)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()

	// The daemon's document is kept verbatim for -json, as dashboard does.
	var raw json.RawMessage
	switch {
	case len(pos) == 0:
		path := "/api/budgets"
		if *period != "" {
			path += "?" + url.Values{"period": {*period}}.Encode()
		}
		err = cl.Do(ctx, "GET", path, nil, &raw)
	case pos[0] == "set" && len(pos) >= 3:
		err = cl.Do(ctx, "PUT", "/api/budgets", map[string]string{
			"category": strings.Join(pos[1:len(pos)-1], " "),
			"period":   *period,
			"planned":  pos[len(pos)-1],
		}, &raw)
	case pos[0] == "copy":
		in := map[string]any{"action": "copy", "period": *period}
		if len(pos) > 1 {
			in["from"] = pos[1]
		}
		err = cl.Do(ctx, "POST", "/api/budgets/helpers", in, &raw)
	case (pos[0] == "average" || pos[0] == "median") && len(pos) >= 1:
		in := map[string]any{"action": pos[0], "period": *period}
		rest := pos[1:]
		if len(rest) > 0 {
			if n, convErr := strconv.Atoi(rest[0]); convErr == nil {
				in["periods"] = n
				rest = rest[1:]
			}
		}
		if len(rest) > 0 {
			in["category"] = strings.Join(rest, " ")
		}
		err = cl.Do(ctx, "POST", "/api/budgets/helpers", in, &raw)
	case pos[0] == "rollover":
		in := map[string]any{"to": *period}
		if len(pos) > 1 {
			in["from"] = pos[1]
		}
		err = cl.Do(ctx, "POST", "/api/budgets/rollover", in, &raw)
		if err == nil && !c.json {
			var e struct {
				Changed int `json:"changed"`
				domain.Envelopes
			}
			if err := json.Unmarshal(raw, &e); err != nil {
				return err
			}
			fmt.Printf("rolled %d pots into %s\n", e.Changed, e.Period)
			return nil
		}
	case pos[0] == "scale" && len(pos) == 2:
		n, convErr := strconv.Atoi(strings.TrimSuffix(pos[1], "%"))
		if convErr != nil {
			return fmt.Errorf("scale wants a percentage, got %q", pos[1])
		}
		err = cl.Do(ctx, "POST", "/api/budgets/helpers", map[string]any{"action": "scale", "period": *period, "percent": n}, &raw)
	default:
		return errors.New("usage: omabudget budget [-period YYYY-MM] [set <category> <amount> | copy [from] | average [3|6|12] [category] | median [12] [category] | scale <percent>]")
	}
	if err != nil {
		return err
	}
	if c.json {
		_, err := os.Stdout.Write(append(raw, '\n'))
		return err
	}

	var b domain.Budget
	if err := json.Unmarshal(raw, &b); err != nil {
		return err
	}
	if len(b.Cards) == 0 && len(b.Unbudgeted) == 0 {
		fmt.Printf("no budget for %s yet: omabudget budget set <category> <amount>\n", b.Period)
		return nil
	}
	cur, err := baseCurrency(ctx, cl)
	if err != nil {
		return err
	}
	f := func(v int64) string { return money.New(v, cur).Format() }
	fmt.Printf("period %s  planned %s %s  spent %s %s  used %d%%\n", b.Period, f(b.Planned), cur, f(b.Spent), cur, b.Pct)
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CATEGORY\tPLANNED\tSPENT\tREMAINING\tUSED\tSTATE")
	for _, card := range b.Cards {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d%%\t%s\n", card.Name, f(card.Planned), f(card.Spent), f(card.Remaining), card.Pct, card.State)
	}
	for _, card := range b.Unbudgeted {
		fmt.Fprintf(tw, "%s\t-\t%s\t\t\tno plan\n", card.Name, f(card.Spent))
	}
	return tw.Flush()
}

// baseCurrency asks the daemon which currency budgets are kept in.
func baseCurrency(ctx context.Context, cl *client.Client) (string, error) {
	var h struct {
		BaseCurrency string `json:"baseCurrency"`
	}
	if err := cl.Do(ctx, "GET", "/api/health", nil, &h); err != nil {
		return "", err
	}
	return h.BaseCurrency, nil
}
