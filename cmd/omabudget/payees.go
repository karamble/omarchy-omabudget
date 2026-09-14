package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/karamble/omarchy-omabudget/client"
	"github.com/karamble/omarchy-omabudget/domain"
)

// payees lists who money went to, spec 1.5. A payee is made by naming one on
// a transaction; these verbs tidy them up afterwards.
//
//	omabudget payees
//	omabudget payee rename "WHOLEFOODS MKT 123" "Whole Foods"
//	omabudget payee alias "Whole Foods" WFM
//	omabudget payee merge "WFM 123" "Whole Foods"
//	omabudget payee category "Whole Foods" Groceries
func runPayees(args []string) error {
	fs := flag.NewFlagSet("payees", flag.ExitOnError)
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
	var list []domain.Payee
	if err := cl.Do(ctx, "GET", "/api/payees", nil, &list); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(list)
	}
	if len(list) == 0 {
		fmt.Println("no payees yet: omabudget add 12.40 groceries -payee \"Whole Foods\"")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PAYEE\tUSED\tUSUALLY\tALSO KNOWN AS")
	for _, p := range list {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", p.Name, p.Uses, p.Category, strings.Join(p.Aliases, ", "))
	}
	return tw.Flush()
}

func runPayee(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: omabudget payee rename|category|notes|alias|unalias|merge|remove ...")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "rename":
		return payeeEdit(rest, "rename", func(v string, in map[string]any) { in["name"] = v })
	case "category":
		return payeeEdit(rest, "category", func(v string, in map[string]any) { in["defaultCategory"] = v })
	case "notes":
		return payeeEdit(rest, "notes", func(v string, in map[string]any) { in["notes"] = v })
	case "alias":
		return payeeEdit(rest, "alias", func(v string, in map[string]any) { in["addAlias"] = v })
	case "unalias":
		return payeeEdit(rest, "unalias", func(v string, in map[string]any) { in["removeAlias"] = v })
	case "merge":
		return payeeEdit(rest, "merge", func(v string, in map[string]any) { in["mergeInto"] = v })
	case "remove":
		return runPayeeRemove(rest)
	case "list":
		return runPayees(rest)
	}
	return fmt.Errorf("unknown payee verb %q", verb)
}

// payeeEdit takes "<payee> <value>" and sends one change.
func payeeEdit(args []string, verb string, apply func(string, map[string]any)) error {
	fs := flag.NewFlagSet("payee "+verb, flag.ExitOnError)
	c := bind(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return fmt.Errorf("usage: omabudget payee %s <payee> <value>", verb)
	}
	in := map[string]any{}
	apply(strings.Join(pos[1:], " "), in)

	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	id, err := payeeID(ctx, cl, pos[0])
	if err != nil {
		return err
	}
	var out struct {
		domain.Payee
		Moved int `json:"moved"`
	}
	if err := cl.Do(ctx, "PUT", "/api/payees/"+url.PathEscape(id), in, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	line := out.Name
	if out.Moved > 0 {
		line += fmt.Sprintf("  %d transactions moved", out.Moved)
	}
	if len(out.Aliases) > 0 {
		line += "  also known as " + strings.Join(out.Aliases, ", ")
	}
	if out.Category != "" {
		line += "  usually " + out.Category
	}
	fmt.Println(line)
	return nil
}

func runPayeeRemove(args []string) error {
	fs := flag.NewFlagSet("payee remove", flag.ExitOnError)
	c := bind(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: omabudget payee remove <payee>")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	id, err := payeeID(ctx, cl, strings.Join(pos, " "))
	if err != nil {
		return err
	}
	var out map[string]string
	if err := cl.Do(ctx, "DELETE", "/api/payees/"+url.PathEscape(id), nil, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	fmt.Printf("removed %s\n", strings.Join(pos, " "))
	return nil
}

// payeeID resolves a name or alias client-side, falling back to the reference
// so the daemon reports what it could not find.
func payeeID(ctx context.Context, cl *client.Client, ref string) (string, error) {
	var list []domain.Payee
	if err := cl.Do(ctx, "GET", "/api/payees", nil, &list); err != nil {
		return "", err
	}
	for _, p := range list {
		if p.ID == ref || strings.EqualFold(p.Name, ref) {
			return p.ID, nil
		}
		for _, a := range p.Aliases {
			if strings.EqualFold(a, ref) {
				return p.ID, nil
			}
		}
	}
	return ref, nil
}
