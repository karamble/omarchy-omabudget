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

// billRow mirrors the API's upcoming row.
type billRow struct {
	domain.Due
	CategoryName string `json:"categoryName"`
	CategoryIcon string `json:"categoryIcon"`
	AccountName  string `json:"accountName"`
}

// bills lists what is due: overdue first, then the next 30 days.
func runBills(args []string) error {
	fs := flag.NewFlagSet("bills", flag.ExitOnError)
	c := bind(fs)
	days := fs.Int("days", 30, "how far ahead to look")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var list []billRow
	if err := cl.Do(ctx, "GET", fmt.Sprintf("/api/bills?days=%d", *days), nil, &list); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(list)
	}
	if len(list) == 0 {
		fmt.Printf("nothing due in the next %d days: omabudget bill add <name> <amount> <category> -every monthly\n", *days)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DUE\tNAME\tAMOUNT\tCATEGORY\tACCOUNT\tSTATE\tRULE")
	for _, b := range list {
		state := fmt.Sprintf("in %d days", b.DaysUntil)
		switch {
		case b.Overdue:
			state = fmt.Sprintf("overdue %d days", -b.DaysUntil)
		case b.DaysUntil == 0:
			state = "today"
		case b.DaysUntil == 1:
			state = "tomorrow"
		}
		if b.AutoPost {
			state += ", auto"
		}
		what := b.CategoryName
		if b.Kind == domain.Transfer {
			what = "transfer"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s %s\t%s\t%s\t%s\t%s\n", b.Date, b.Name,
			money.New(b.Amount, b.Currency).Format(), b.Currency, what, b.AccountName, state, b.RuleID)
	}
	return tw.Flush()
}

// bill is the rule verbs: add, edit, list, post, skip, remove.
func runBill(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: omabudget bill add|edit|list|post|skip|remove ...")
	}
	switch args[0] {
	case "add":
		return runBillAdd(args[1:])
	case "edit":
		return runBillEdit(args[1:])
	case "list":
		return runBillList(args[1:])
	case "post":
		return runBillPost(args[1:])
	case "skip":
		return runIDVerb(args[1:], "bill skip", "POST", "/api/rules/%s/skip", "skipped")
	case "remove":
		return runIDVerb(args[1:], "bill remove", "DELETE", "/api/rules/%s", "removed")
	}
	return fmt.Errorf("unknown bill verb %q", args[0])
}

// ruleFlags are shared by add and edit; set records which were given.
type ruleFlags struct {
	every, day, start, end, account, transferTo, desc, tags string
	interval, count, lead                                   int
	income, expense, auto, variable                         bool
	set                                                     map[string]bool
}

func bindRuleFlags(fs *flag.FlagSet) *ruleFlags {
	f := &ruleFlags{set: map[string]bool{}}
	fs.StringVar(&f.every, "every", "monthly", "daily, weekly, biweekly, monthly, quarterly, semiannual, annual or custom")
	fs.IntVar(&f.interval, "interval-days", 0, "days between occurrences, for -every custom")
	fs.StringVar(&f.day, "day", "", "\"last\" to fall on the last day of the month")
	fs.StringVar(&f.start, "start", "", "first due date, YYYY-MM-DD (default: today)")
	fs.StringVar(&f.end, "end", "", "last possible date, YYYY-MM-DD")
	fs.IntVar(&f.count, "count", 0, "stop after this many occurrences")
	fs.StringVar(&f.account, "account", "", "account name or id (default: the first account)")
	fs.StringVar(&f.transferTo, "transfer-to", "", "make each occurrence a transfer into this account")
	fs.BoolVar(&f.income, "income", false, "money coming in")
	fs.BoolVar(&f.expense, "expense", false, "money going out, the default for a new rule")
	fs.BoolVar(&f.auto, "auto", false, "post on the due date without asking")
	fs.IntVar(&f.lead, "lead", 3, "days of notice before the due date")
	fs.BoolVar(&f.variable, "variable", false, "the amount changes; confirm it when posting")
	fs.StringVar(&f.desc, "desc", "", "description of each occurrence (default: the name)")
	fs.StringVar(&f.tags, "tag", "", "comma separated tags")
	return f
}

func (f *ruleFlags) apply(in map[string]any) {
	if f.set["every"] {
		in["frequency"] = f.every
	}
	if f.set["interval-days"] {
		in["intervalDays"] = f.interval
	}
	if f.set["day"] {
		in["dayRule"] = f.day
	}
	if f.set["start"] {
		in["startDate"] = f.start
	}
	if f.set["end"] {
		in["endDate"] = f.end
	}
	if f.set["count"] {
		in["occurrenceCount"] = f.count
	}
	if f.set["account"] {
		in["account"] = f.account
	}
	switch {
	case f.transferTo != "":
		in["kind"], in["counterAccount"], in["category"] = "transfer", f.transferTo, ""
	case f.income:
		in["kind"], in["counterAccount"] = "income", ""
	case f.expense:
		in["kind"], in["counterAccount"] = "expense", ""
	}
	if f.set["auto"] {
		in["autoPost"] = f.auto
	}
	if f.set["lead"] {
		in["leadDays"] = f.lead
	}
	if f.set["variable"] {
		in["variableAmount"] = f.variable
	}
	if f.set["desc"] {
		in["description"] = f.desc
	}
	if f.set["tag"] {
		in["tags"] = []string{}
		if f.tags != "" {
			in["tags"] = strings.Split(f.tags, ",")
		}
	}
}

// bill add <name> <amount> [category] [flags]
//
//	omabudget bill add Rent 1200 Rent -every monthly -start 2026-10-03 -auto
//	omabudget bill add Gym 29.90 "Sport & fitness" -every monthly -day last
func runBillAdd(args []string) error {
	fs := flag.NewFlagSet("bill add", flag.ExitOnError)
	c := bind(fs)
	f := bindRuleFlags(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
	if len(pos) < 2 {
		return errors.New("usage: omabudget bill add <name> <amount> [category] [flags]")
	}
	in := map[string]any{
		"name": pos[0], "amount": pos[1], "kind": "expense", "frequency": f.every,
		"autoPost": f.auto, "leadDays": f.lead, "variableAmount": f.variable,
	}
	if len(pos) > 2 {
		in["category"] = strings.Join(pos[2:], " ")
	}
	f.apply(in)
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out domain.Rule
	if err := cl.Do(ctx, "POST", "/api/rules", in, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	fmt.Printf("%s  %s %s  %s  next %s  (%s)\n", out.Name, money.New(out.Template.Amount, out.Template.Currency).Format(),
		out.Template.Currency, out.Frequency, out.NextDue, out.ID)
	return nil
}

// bill edit <id> [flags]: reads the rule back and sends it with the changes.
func runBillEdit(args []string) error {
	fs := flag.NewFlagSet("bill edit", flag.ExitOnError)
	c := bind(fs)
	f := bindRuleFlags(fs)
	name := fs.String("name", "", "new name")
	amount := fs.String("amount", "", "new amount")
	category := fs.String("category", "", "new category")
	pause := fs.Bool("pause", false, "stop posting until resumed")
	resume := fs.Bool("resume", false, "start posting again")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
	if len(pos) != 1 {
		return errors.New("usage: omabudget bill edit <id> [flags]")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var cur domain.Rule
	var all []domain.Rule
	if err := cl.Do(ctx, "GET", "/api/rules?all=1", nil, &all); err != nil {
		return err
	}
	found := false
	for _, r := range all {
		if r.ID == pos[0] || strings.EqualFold(r.Name, pos[0]) {
			cur, found = r, true
		}
	}
	if !found {
		return fmt.Errorf("no rule %q", pos[0])
	}
	in := map[string]any{
		"name": cur.Name, "kind": string(cur.Template.Kind), "account": cur.Template.AccountID,
		"counterAccount": cur.Template.CounterAccountID,
		"amount":         money.New(cur.Template.Amount, cur.Template.Currency).Format(),
		"category":       cur.Template.CategoryID, "description": cur.Template.Description, "tags": cur.Template.Tags,
		"frequency": cur.Frequency, "intervalDays": cur.IntervalDays, "dayRule": cur.DayRule,
		"startDate": cur.StartDate, "endDate": cur.EndDate, "occurrenceCount": cur.OccurrenceCount,
		"autoPost": cur.AutoPost, "leadDays": cur.LeadDays, "variableAmount": cur.VariableAmount,
		"active": cur.Active,
	}
	if f.set["name"] {
		in["name"] = *name
	}
	if f.set["amount"] {
		in["amount"] = *amount
	}
	if f.set["category"] {
		in["category"] = *category
	}
	if *pause {
		in["active"] = false
	}
	if *resume {
		in["active"] = true
	}
	f.apply(in)
	var out domain.Rule
	if err := cl.Do(ctx, "PUT", "/api/rules/"+url.PathEscape(cur.ID), in, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	state := "next " + out.NextDue
	if !out.Active {
		state = "paused"
	}
	fmt.Printf("%s  %s %s  %s  %s  (%s)\n", out.Name, money.New(out.Template.Amount, out.Template.Currency).Format(),
		out.Template.Currency, out.Frequency, state, out.ID)
	return nil
}

func runBillList(args []string) error {
	fs := flag.NewFlagSet("bill list", flag.ExitOnError)
	c := bind(fs)
	all := fs.Bool("all", false, "paused and finished rules too")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	path := "/api/rules"
	if *all {
		path += "?all=1"
	}
	var list []domain.Rule
	if err := cl.Do(ctx, "GET", path, nil, &list); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(list)
	}
	if len(list) == 0 {
		fmt.Println("no rules yet: omabudget bill add <name> <amount> <category> -every monthly")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tAMOUNT\tEVERY\tNEXT\tPOSTED\tAUTO\tID")
	for _, r := range list {
		next := r.NextDue
		if !r.Active {
			next = "paused"
		}
		auto := ""
		if r.AutoPost {
			auto = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s %s\t%s\t%s\t%d\t%s\t%s\n", r.Name,
			money.New(r.Template.Amount, r.Template.Currency).Format(), r.Template.Currency,
			r.Frequency, next, r.Posted, auto, r.ID)
	}
	return tw.Flush()
}

// bill post <id> [-date YYYY-MM-DD] [-amount X]
func runBillPost(args []string) error {
	fs := flag.NewFlagSet("bill post", flag.ExitOnError)
	c := bind(fs)
	date := fs.String("date", "", "the date it was paid (default: the due date)")
	amount := fs.String("amount", "", "the amount paid, when it differs")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: omabudget bill post <id> [-date] [-amount]")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	in := map[string]any{}
	if *date != "" {
		in["date"] = *date
	}
	if *amount != "" {
		in["amount"] = *amount
	}
	var out domain.Transaction
	if err := cl.Do(ctx, "POST", "/api/rules/"+url.PathEscape(pos[0])+"/post", in, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	fmt.Printf("posted %s  %s %s  %s  (%s)\n", out.Date, money.New(out.Amount, out.Currency).Format(), out.Currency, out.Description, out.ID)
	return nil
}
