package main

import (
	"context"
	"encoding/json"
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

// runAdd is quick-add from the command line, spec 4.1: amount and category,
// everything else optional.
//
//	omabudget add 4.50 coffee
//	omabudget add 23.50+18 groceries -desc "Biedronka" -tag weekly
//	omabudget add 3250 "Primary salary" -income
//	omabudget add 500 -transfer-to Savings
//
// repeated collects a flag given more than once, in order.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ", ") }

func (r *repeated) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// parseSplits reads "<category>=<amount>[:note]" lines into the shape the
// daemon takes. The amounts are magnitudes; the sign follows the parent, and
// the daemon refuses a set that does not add up to it.
func parseSplits(specs []string) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		cat, rest, found := strings.Cut(spec, "=")
		cat = strings.TrimSpace(cat)
		if !found || cat == "" || strings.TrimSpace(rest) == "" {
			return nil, fmt.Errorf("split %q: write it as <category>=<amount> with an optional :note", spec)
		}
		amount, note, _ := strings.Cut(rest, ":")
		line := map[string]any{"category": cat, "amount": strings.TrimSpace(amount)}
		if strings.TrimSpace(note) != "" {
			line["note"] = strings.TrimSpace(note)
		}
		out = append(out, line)
	}
	return out, nil
}

func runAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	c := bind(fs)
	account := fs.String("account", "", "account name or id (default: the first account)")
	income := fs.Bool("income", false, "money coming in rather than going out")
	transferTo := fs.String("transfer-to", "", "make this a transfer into the named account")
	received := fs.String("received", "", "amount received on a cross-currency transfer")
	date := fs.String("date", "", "YYYY-MM-DD (default: today)")
	desc := fs.String("desc", "", "description")
	notes := fs.String("notes", "", "notes")
	tags := fs.String("tag", "", "comma separated tags")
	payee := fs.String("payee", "", "who it was paid to; a new name is remembered")
	rate := fs.String("rate", "", "exchange rate to the base currency, for a foreign-currency account")
	status := fs.String("status", "", "pending, cleared (the default) or reconciled")
	var splits repeated
	fs.Var(&splits, "split", "a line of a split, <category>=<amount>[:note]; repeat it for each line")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return errors.New("usage: omabudget add <amount> [category] [flags]")
	}
	if len(splits) > 0 && len(pos) > 1 {
		return errors.New("a split takes its categories from -split, not from a positional one")
	}

	in := map[string]any{
		"amount":      pos[0],
		"account":     *account,
		"date":        *date,
		"description": *desc,
		"notes":       *notes,
		"payee":       *payee,
	}
	// A rate is sent only when one was typed: a rate on the row is a record
	// of what was charged, and it is kept when the table is corrected.
	if *rate != "" {
		in["fxRate"] = *rate
	}
	switch {
	case *transferTo != "":
		in["kind"] = "transfer"
		in["counterAccount"] = *transferTo
		if *received != "" {
			in["counterAmount"] = *received
		}
	case *income:
		in["kind"] = "income"
	default:
		in["kind"] = "expense"
	}
	if len(pos) > 1 {
		in["category"] = strings.Join(pos[1:], " ")
	}
	if *tags != "" {
		in["tags"] = strings.Split(*tags, ",")
	}
	if *status != "" {
		in["status"] = *status
	}
	if len(splits) > 0 {
		lines, err := parseSplits(splits)
		if err != nil {
			return err
		}
		in["splits"] = lines
	}

	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out domain.Transaction
	if err := cl.Do(ctx, "POST", "/api/transactions", in, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	what := out.CategoryID
	if out.Kind == domain.Transfer {
		what = "transfer"
	}
	fmt.Printf("%s  %s %s  %s  (%s)\n", out.Date, money.New(out.Amount, out.Currency).Format(), out.Currency, what, out.ID)
	return nil
}

func runAddAccount(args []string) error {
	fs := flag.NewFlagSet("add-account", flag.ExitOnError)
	c := bind(fs)
	typ := fs.String("type", "checking", "checking, savings, cash, credit_card, loan, investment, prepaid, receivable or payable")
	currency := fs.String("currency", "", "three-letter code (default: the base currency)")
	opening := fs.String("opening", "", "opening balance, as an amount")
	openingDate := fs.String("opening-date", "", "YYYY-MM-DD (default: today)")
	institution := fs.String("institution", "", "bank or issuer, free text")
	last4 := fs.String("last4", "", "display-only label, never a full number")
	lowBalance := fs.String("low-balance", "", "warn under this amount")
	networth := fs.Bool("networth", true, "-networth=false keeps it out of net worth")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return errors.New("usage: omabudget add-account <name> [flags]")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	cur := *currency
	if cur == "" {
		var h struct {
			BaseCurrency string `json:"baseCurrency"`
		}
		if err := cl.Do(ctx, "GET", "/api/health", nil, &h); err != nil {
			return err
		}
		cur = h.BaseCurrency
	}
	var out domain.Account
	if err := cl.Do(ctx, "POST", "/api/accounts", map[string]any{
		"name": strings.Join(pos, " "), "type": *typ, "currency": cur,
		"openingBalance": *opening, "openingDate": *openingDate,
		"institution": *institution, "last4": *last4,
		"lowBalance": *lowBalance, "includeInNetWorth": *networth,
	}, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	fmt.Printf("added %s (%s, %s) id %s\n", out.Name, out.Type, out.Currency, out.ID)
	return nil
}

type accountRow struct {
	domain.Account
	Balance int64 `json:"balance"`
}

func runAccounts(args []string) error {
	fs := flag.NewFlagSet("accounts", flag.ExitOnError)
	c := bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var rows []accountRow
	if err := cl.Do(ctx, "GET", "/api/accounts", nil, &rows); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(rows)
	}
	if len(rows) == 0 {
		fmt.Println("no accounts yet: omabudget add-account <name>")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ACCOUNT\tTYPE\tBALANCE\tID")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s %s\t%s\n", r.Name, r.Type, money.New(r.Balance, r.Currency).Format(), r.Currency, r.ID)
	}
	return tw.Flush()
}

func runCategories(args []string) error {
	fs := flag.NewFlagSet("categories", flag.ExitOnError)
	c := bind(fs)
	all := fs.Bool("all", false, "archived categories too")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var cats []domain.Category
	if err := cl.Do(ctx, "GET", "/api/categories", nil, &cats); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(cats)
	}
	for _, cat := range cats {
		if cat.Archived && !*all {
			continue
		}
		flags := ""
		if cat.Archived {
			flags += "  archived"
		}
		if cat.Excluded {
			flags += "  not in statistics"
		}
		if cat.ParentID == "" {
			fmt.Printf("%s %s  [%s]%s\n", cat.Icon, cat.Name, cat.Kind, flags)
			continue
		}
		fmt.Printf("    %s%s\n", cat.Name, flags)
	}
	return nil
}

func runTransactions(args []string) error {
	fs := flag.NewFlagSet("transactions", flag.ExitOnError)
	c := bind(fs)
	account := fs.String("account", "", "only this account")
	category := fs.String("category", "", "only this category id")
	from := fs.String("from", "", "YYYY-MM-DD, inclusive")
	to := fs.String("to", "", "YYYY-MM-DD, inclusive")
	limit := fs.Int("limit", 50, "at most this many")
	offset := fs.Int("offset", 0, "skip this many first")
	search := fs.String("q", "", "text in the description, notes or payee")
	kind := fs.String("kind", "", "expense, income or transfer")
	status := fs.String("status", "", "pending, cleared or reconciled")
	deleted := fs.Bool("deleted", false, "the recently deleted ones, for undo")
	payee := fs.String("payee", "", "only this payee, by name or alias")
	minAmount := fs.String("min", "", "at least this much, whatever the sign")
	maxAmount := fs.String("max", "", "at most this much")
	var tags repeated
	fs.Var(&tags, "tag", "carrying this tag; repeat it for any of several")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	q := url.Values{}
	if *account != "" {
		q.Set("account", *account)
	}
	if *category != "" {
		q.Set("category", *category)
	}
	if *from != "" {
		q.Set("from", *from)
	}
	if *to != "" {
		q.Set("to", *to)
	}
	q.Set("limit", fmt.Sprint(*limit))
	if *offset > 0 {
		q.Set("offset", fmt.Sprint(*offset))
	}
	if *search != "" {
		q.Set("q", *search)
	}
	if *kind != "" {
		q.Set("kind", *kind)
	}
	if *status != "" {
		q.Set("status", *status)
	}
	if *deleted {
		q.Set("deleted", "1")
	}
	if *payee != "" {
		q.Set("payee", *payee)
	}
	if *minAmount != "" {
		q.Set("min", *minAmount)
	}
	if *maxAmount != "" {
		q.Set("max", *maxAmount)
	}
	for _, tag := range tags {
		q.Add("tag", tag)
	}
	var list []domain.Transaction
	if err := cl.Do(ctx, "GET", "/api/transactions?"+q.Encode(), nil, &list); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(list)
	}
	if len(list) == 0 {
		if *deleted {
			fmt.Println("nothing deleted in the last 30 days")
		} else {
			fmt.Println("nothing yet: omabudget add <amount> <category>")
		}
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DATE\tAMOUNT\tKIND\tCATEGORY\tDESCRIPTION\tID")
	for _, t := range list {
		what := t.CategoryID
		if t.Kind == domain.Transfer {
			what = "-> " + t.CounterAccountID
		}
		fmt.Fprintf(tw, "%s\t%s %s\t%s\t%s\t%s\t%s\n", t.Date,
			money.New(t.Amount, t.Currency).Format(), t.Currency, t.Kind, what, t.Description, t.ID)
	}
	return tw.Flush()
}

func runShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	c := bind(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: omabudget show <id>")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var t domain.Transaction
	if err := cl.Do(ctx, "GET", "/api/transactions/"+url.PathEscape(pos[0]), nil, &t); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(t)
	}
	printTransaction(t)
	return nil
}

func printTransaction(t domain.Transaction) {
	fmt.Printf("%s  %s %s  %s\n", t.Date, money.New(t.Amount, t.Currency).Format(), t.Currency, t.Kind)
	fmt.Printf("account      %s\n", t.AccountID)
	if t.Kind == domain.Transfer {
		fmt.Printf("to           %s\n", t.CounterAccountID)
	} else if t.CategoryID != "" {
		fmt.Printf("category     %s\n", t.CategoryID)
	}
	if t.PayeeName != "" {
		fmt.Printf("payee        %s\n", t.PayeeName)
	}
	if t.Description != "" {
		fmt.Printf("description  %s\n", t.Description)
	}
	if t.Notes != "" {
		fmt.Printf("notes        %s\n", t.Notes)
	}
	if len(t.Tags) > 0 {
		fmt.Printf("tags         %s\n", strings.Join(t.Tags, ", "))
	}
	for _, sp := range t.Splits {
		fmt.Printf("line         %s %s  %s  %s\n", money.New(sp.Amount, t.Currency).Format(), t.Currency, sp.CategoryID, sp.Note)
	}
	fmt.Printf("status       %s\n", t.Status)
	if t.DeletedAt != "" {
		fmt.Printf("deleted      %s  (omabudget undo %s)\n", t.DeletedAt, t.ID)
	}
	fmt.Printf("id           %s\n", t.ID)
}

// edit reads the transaction back, applies the flags that were given and
// sends the whole form again, so the daemon validates it like a new entry.
//
//	omabudget edit <id> -amount 12.80 -desc "Lunch"
//	omabudget edit <id> -category Fuel -tag car,work
func runEdit(args []string) error {
	fs := flag.NewFlagSet("edit", flag.ExitOnError)
	c := bind(fs)
	amount := fs.String("amount", "", "new amount, arithmetic allowed")
	category := fs.String("category", "", "new category")
	account := fs.String("account", "", "move it to this account")
	transferTo := fs.String("transfer-to", "", "make it a transfer into this account")
	received := fs.String("received", "", "amount received on a cross-currency transfer")
	income := fs.Bool("income", false, "make it income")
	expense := fs.Bool("expense", false, "make it an expense")
	date := fs.String("date", "", "YYYY-MM-DD")
	desc := fs.String("desc", "", "description")
	notes := fs.String("notes", "", "notes")
	tags := fs.String("tag", "", "comma separated tags, replacing the old ones")
	addTags := fs.String("add-tag", "", "comma separated tags to add, keeping the old ones")
	payee := fs.String("payee", "", "who it was paid to; empty clears it")
	rate := fs.String("rate", "", "exchange rate to the base currency")
	status := fs.String("status", "", "pending, cleared or reconciled")
	var splits repeated
	fs.Var(&splits, "split", "replace the split lines; <category>=<amount>[:note], repeatable")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: omabudget edit <id>... [flags]")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	// Split lines belong to one transaction's own amount, so they cannot be
	// applied to a handful at once.
	if len(pos) > 1 && set["split"] {
		return errors.New("-split edits one transaction at a time")
	}

	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	for _, id := range pos {
		if err := editOne(ctx, cl, c, id, set, editFlags{
			amount: amount, category: category, account: account, transferTo: transferTo,
			received: received, income: income, expense: expense, date: date, desc: desc,
			notes: notes, tags: tags, addTags: addTags, payee: payee, rate: rate,
			status: status, splits: splits,
		}); err != nil {
			return err
		}
	}
	return nil
}

// editFlags is what one -flag set means for each id given.
type editFlags struct {
	amount, category, account, transferTo, received *string
	income, expense                                 *bool
	date, desc, notes, tags, payee, rate, status    *string
	addTags                                         *string
	splits                                          repeated
}

func editOne(ctx context.Context, cl *client.Client, c *common, id string, set map[string]bool, f editFlags) error {
	amount, category, account := f.amount, f.category, f.account
	transferTo, received := f.transferTo, f.received
	income, expense := f.income, f.expense
	date, desc, notes, tags := f.date, f.desc, f.notes, f.tags
	payee, rate, status, splits := f.payee, f.rate, f.status, f.splits
	addTags := f.addTags

	var cur domain.Transaction
	if err := cl.Do(ctx, "GET", "/api/transactions/"+url.PathEscape(id), nil, &cur); err != nil {
		return err
	}

	// The stored rate is not echoed back: the daemon keeps a rate the row
	// carries, and sending it would turn every edit into a typed rate.
	in := map[string]any{
		"kind": string(cur.Kind), "account": cur.AccountID, "counterAccount": cur.CounterAccountID,
		"amount": money.New(cur.Amount, cur.Currency).Abs().Format(), "currency": cur.Currency,
		"category": cur.CategoryID, "date": cur.Date,
		"description": cur.Description, "notes": cur.Notes, "tags": cur.Tags, "status": string(cur.Status),
		"payee": cur.PayeeName,
	}
	if cur.CounterAmount != nil {
		var accounts []domain.Account
		if err := cl.Do(ctx, "GET", "/api/accounts", nil, &accounts); err != nil {
			return err
		}
		for _, a := range accounts {
			if a.ID == cur.CounterAccountID {
				in["counterAmount"] = money.New(*cur.CounterAmount, a.Currency).Abs().Format()
			}
		}
	}
	var lines []map[string]any
	for _, sp := range cur.Splits {
		lines = append(lines, map[string]any{"category": sp.CategoryID, "amount": money.New(sp.Amount, cur.Currency).Abs().Format(), "note": sp.Note})
	}
	if len(lines) > 0 {
		in["splits"] = lines
	}

	if set["amount"] {
		in["amount"] = *amount
	}
	if set["category"] {
		in["category"] = *category
	}
	if set["account"] {
		in["account"] = *account
	}
	switch {
	case *transferTo != "":
		in["kind"], in["counterAccount"], in["category"] = "transfer", *transferTo, ""
	case *income:
		in["kind"], in["counterAccount"] = "income", ""
	case *expense:
		in["kind"], in["counterAccount"] = "expense", ""
	}
	if set["received"] {
		in["counterAmount"] = *received
	}
	if set["date"] {
		in["date"] = *date
	}
	if set["desc"] {
		in["description"] = *desc
	}
	if set["notes"] {
		in["notes"] = *notes
	}
	if set["tag"] {
		in["tags"] = []string{}
		if *tags != "" {
			in["tags"] = strings.Split(*tags, ",")
		}
	}
	// -add-tag keeps what is there, which is what tagging a handful means.
	if set["add-tag"] {
		have := append([]string{}, cur.Tags...)
		for _, t := range strings.Split(*addTags, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			seen := false
			for _, existing := range have {
				if strings.EqualFold(existing, t) {
					seen = true
					break
				}
			}
			if !seen {
				have = append(have, t)
			}
		}
		in["tags"] = have
	}
	if set["payee"] {
		in["payee"] = *payee
	}
	if set["rate"] {
		in["fxRate"] = *rate
	}
	if set["status"] {
		in["status"] = *status
	}
	// -split replaces the whole set; a single category clears the lines,
	// since a transaction is one or the other.
	switch {
	case set["split"] && set["category"]:
		return errors.New("give -split or -category, not both")
	case set["split"]:
		lines, err := parseSplits(splits)
		if err != nil {
			return err
		}
		in["splits"] = lines
	case set["category"]:
		delete(in, "splits")
	}

	var out domain.Transaction
	if err := cl.Do(ctx, "PUT", "/api/transactions/"+url.PathEscape(cur.ID), in, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	what := out.CategoryID
	if out.Kind == domain.Transfer {
		what = "transfer"
	}
	fmt.Printf("%s  %s %s  %s  (%s)\n", out.Date, money.New(out.Amount, out.Currency).Format(), out.Currency, what, out.ID)
	return nil
}

// edit-account sends only the flags that were given.
//
//	omabudget edit-account Checking -name Everyday -institution "House bank"
//	omabudget edit-account Cash -active=false
func runEditAccount(args []string) error {
	fs := flag.NewFlagSet("edit-account", flag.ExitOnError)
	c := bind(fs)
	name := fs.String("name", "", "new name")
	typ := fs.String("type", "", "account type, only while the account has no postings")
	institution := fs.String("institution", "", "bank or issuer, free text")
	last4 := fs.String("last4", "", "display-only label, never a full number")
	active := fs.Bool("active", true, "-active=false closes the account")
	networth := fs.Bool("networth", true, "-networth=false keeps it out of net worth")
	lowBalance := fs.String("low-balance", "", "warn under this amount; empty clears it")
	opening := fs.String("opening", "", "opening balance, as an amount")
	openingDate := fs.String("opening-date", "", "YYYY-MM-DD")
	sort := fs.Int("sort", 0, "position in lists, lowest first")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: omabudget edit-account <name or id> [flags]")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if len(set) == 0 {
		return errors.New("nothing to change: give at least one flag")
	}

	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var accounts []domain.Account
	if err := cl.Do(ctx, "GET", "/api/accounts", nil, &accounts); err != nil {
		return err
	}
	id := ""
	for _, a := range accounts {
		if a.ID == pos[0] || strings.EqualFold(a.Name, pos[0]) {
			id = a.ID
		}
	}
	if id == "" {
		return fmt.Errorf("no account named %q", pos[0])
	}

	in := map[string]any{}
	if set["name"] {
		in["name"] = *name
	}
	if set["type"] {
		in["type"] = *typ
	}
	if set["institution"] {
		in["institution"] = *institution
	}
	if set["last4"] {
		in["last4"] = *last4
	}
	if set["active"] {
		in["active"] = *active
	}
	if set["networth"] {
		in["includeInNetWorth"] = *networth
	}
	if set["low-balance"] {
		in["lowBalance"] = *lowBalance
	}
	if set["opening"] {
		in["opening"] = *opening
	}
	if set["opening-date"] {
		in["openingDate"] = *openingDate
	}
	if set["sort"] {
		in["sortOrder"] = *sort
	}
	var out struct {
		domain.Account
		Balance int64 `json:"balance"`
	}
	if err := cl.Do(ctx, "PUT", "/api/accounts/"+url.PathEscape(id), in, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	state := ""
	if !out.Active {
		state = "  (closed)"
	}
	fmt.Printf("%s  %s %s%s\n", out.Name, money.New(out.Balance, out.Currency).Format(), out.Currency, state)
	return nil
}

func runDelete(args []string) error {
	return runIDVerb(args, "delete", "DELETE", "/api/transactions/%s", "deleted")
}

func runUndo(args []string) error {
	return runIDVerb(args, "undo", "POST", "/api/transactions/%s/restore", "restored")
}

func runIDVerb(args []string, name, method, pathFmt, done string) error {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	c := bind(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("usage: omabudget %s <id>...", name)
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	all := []map[string]string{}
	for _, id := range pos {
		var out map[string]string
		if err := cl.Do(ctx, method, fmt.Sprintf(pathFmt, id), nil, &out); err != nil {
			return err
		}
		all = append(all, out)
		if !c.json {
			fmt.Println(done, id)
		}
	}
	if c.json {
		if len(all) == 1 {
			return client.PrintJSON(all[0])
		}
		return client.PrintJSON(all)
	}
	return nil
}

func runDashboard(args []string) error {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	c := bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()

	// With -json the daemon's document is printed verbatim. Re-typing it here
	// would rename fields the panel reads by the API's names.
	if c.json {
		var raw json.RawMessage
		if err := cl.Do(ctx, "GET", "/api/dashboard", nil, &raw); err != nil {
			return err
		}
		_, err := os.Stdout.Write(append(raw, '\n'))
		return err
	}

	var out struct {
		BaseCurrency string `json:"baseCurrency"`
		Period       struct {
			From    string `json:"from"`
			To      string `json:"to"`
			Days    int    `json:"days"`
			Elapsed int    `json:"elapsed"`
		} `json:"period"`
		Totals struct {
			Income      int64 `json:"income"`
			Expense     int64 `json:"expense"`
			Net         int64 `json:"net"`
			SavingsRate int   `json:"savingsRate"`
		} `json:"totals"`
		Liquid   int64        `json:"liquid"`
		NetWorth int64        `json:"netWorth"`
		Accounts []accountRow `json:"accounts"`
		Insights []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
			Tone string `json:"tone"`
		} `json:"insights"`
	}
	if err := cl.Do(ctx, "GET", "/api/dashboard", nil, &out); err != nil {
		return err
	}
	cur := out.BaseCurrency
	f := func(v int64) string { return money.New(v, cur).Format() + " " + cur }
	fmt.Printf("period %s to %s  (day %d of %d)\n", out.Period.From, out.Period.To, out.Period.Elapsed, out.Period.Days)
	fmt.Printf("income   %s\nexpense  %s\nnet      %s  savings rate %d%%\n", f(out.Totals.Income), f(out.Totals.Expense), f(out.Totals.Net), out.Totals.SavingsRate)
	fmt.Printf("liquid   %s\nnet worth %s\n", f(out.Liquid), f(out.NetWorth))
	if len(out.Insights) > 0 {
		fmt.Println()
		for _, in := range out.Insights {
			fmt.Printf("  %s\n", in.Text)
		}
	}
	if len(out.Accounts) > 0 {
		fmt.Println()
		for _, a := range out.Accounts {
			fmt.Printf("  %-24s %s %s\n", a.Name, money.New(a.Balance, a.Currency).Format(), a.Currency)
		}
	}
	return nil
}

// remove-account deletes an account nothing points at, which is how a typo at
// creation is undone. One with history is closed instead, and the daemon says
// so.
//
//	omabudget remove-account "Savigns"
func runRemoveAccount(args []string) error {
	fs := flag.NewFlagSet("remove-account", flag.ExitOnError)
	c := bind(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: omabudget remove-account <account>")
	}
	ref := strings.Join(pos, " ")
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out map[string]string
	if err := cl.Do(ctx, "DELETE", "/api/accounts/"+url.PathEscape(ref), nil, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	fmt.Printf("removed %s\n", ref)
	return nil
}
