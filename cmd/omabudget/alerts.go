package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"strings"

	"github.com/karamble/omarchy-omabudget/alerts"
	"github.com/karamble/omarchy-omabudget/client"
)

// arm puts a watch on one of the catalogue's paths. Until now only an agent
// over MCP could do this.
//
//	omabudget arm budget.over appears -reason "tell me when a category goes over"
//	omabudget arm bills.overdue count -above 0 -expires-in 720h
//	omabudget arm period.savingsRate crosses -below 10
//	omabudget arm ledger.large appears -where "account=Checking"
func runArm(args []string) error {
	fs := flag.NewFlagSet("arm", flag.ExitOnError)
	c := bind(fs)
	spec, where := bindAlertFlags(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New("usage: omabudget arm <path> <appears|disappears|count|crosses|becomes|ages> [flags]")
	}
	spec.Path, spec.Operator = pos[0], pos[1]
	if err := applyWhere(spec, *where); err != nil {
		return err
	}
	return alertDo(c, "POST", "/api/alerts", spec)
}

// alert edit <id> replaces a watch's terms; alert list and alert disarm are
// the same as the alerts and disarm verbs.
func runAlert(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: omabudget alert edit <id> ..., or: omabudget alerts, arm, disarm")
	}
	switch args[0] {
	case "edit":
		return runAlertEdit(args[1:])
	case "list":
		return runGet(args[1:], "/api/alerts")
	case "disarm":
		return runDisarm(args[1:])
	}
	return fmt.Errorf("unknown alert verb %q: edit, list or disarm", args[0])
}

func runAlertEdit(args []string) error {
	fs := flag.NewFlagSet("alert edit", flag.ExitOnError)
	c := bind(fs)
	spec, where := bindAlertFlags(fs)
	path := fs.String("path", "", "a different catalogue path")
	operator := fs.String("operator", "", "a different operator")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: omabudget alert edit <id> [flags]")
	}
	var cur []alerts.Trigger
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	if err := cl.Do(ctx, "GET", "/api/alerts", nil, &cur); err != nil {
		return err
	}
	found := false
	for _, t := range cur {
		if t.ID != pos[0] {
			continue
		}
		found = true
		if spec.Path == "" {
			spec.Path = t.Path
		}
		if spec.Operator == "" {
			spec.Operator = string(t.Operator)
		}
		if len(*where) == 0 {
			spec.Where = t.Where
		}
		if spec.DeliverTo == "" {
			spec.DeliverTo = t.DeliverTo
		}
		if spec.Reason == "" {
			spec.Reason = t.Reason
		}
	}
	if !found {
		return fmt.Errorf("no watch %q", pos[0])
	}
	if *path != "" {
		spec.Path = *path
	}
	if *operator != "" {
		spec.Operator = *operator
	}
	if err := applyWhere(spec, *where); err != nil {
		return err
	}
	return alertDo(c, "PUT", "/api/alerts/"+url.PathEscape(pos[0]), spec)
}

// bindAlertFlags is the terms a watch takes, shared by arm and edit.
func bindAlertFlags(fs *flag.FlagSet) (*alerts.Spec, *repeated) {
	spec := &alerts.Spec{}
	var where repeated
	fs.Var(&where, "where", "narrow a list path: field=value or field~=text; repeatable")
	fs.StringVar(&spec.DeliverTo, "deliver-to", "", "where the alarm goes (default: you, the desktop)")
	fs.StringVar(&spec.ExpiresIn, "expires-in", "", "how long it stays armed, such as 24h or 168h (default: 168h)")
	fs.BoolVar(&spec.Standing, "standing", false, "fire every time it happens, not once")
	fs.StringVar(&spec.Reason, "reason", "", "why it exists, shown with the alarm")
	fs.StringVar(&spec.Params.Value, "value", "", "for becomes: the value to wake on")
	fs.StringVar(&spec.Params.Field, "field", "", "for ages: the timestamp field")
	fs.StringVar(&spec.Params.OlderThan, "older-than", "", "for ages: how old, such as 72h")
	fs.StringVar(&spec.Params.Hold, "hold", "", "for becomes: how long it must stay away before ringing again")
	fs.Func("above", "for count and crosses: the bound to pass upward", func(v string) error {
		f, err := parseBound(v)
		spec.Params.Above = f
		return err
	})
	fs.Func("below", "for count and crosses: the bound to pass downward", func(v string) error {
		f, err := parseBound(v)
		spec.Params.Below = f
		return err
	})
	fs.Func("rearm", "how far back past the bound it must come before it can fire again", func(v string) error {
		f, err := parseBound(v)
		spec.Params.Rearm = f
		return err
	})
	return spec, &where
}

func parseBound(v string) (*float64, error) {
	var f float64
	if _, err := fmt.Sscanf(strings.TrimSpace(v), "%g", &f); err != nil {
		return nil, fmt.Errorf("%q is not a number", v)
	}
	return &f, nil
}

func applyWhere(spec *alerts.Spec, where repeated) error {
	if len(where) == 0 {
		return nil
	}
	parsed, err := alerts.ParseWhereAll(where)
	if err != nil {
		return err
	}
	spec.Where = parsed
	return nil
}

func alertDo(c *common, method, path string, spec *alerts.Spec) error {
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out alerts.Trigger
	if err := cl.Do(ctx, method, path, spec, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	line := out.Path + " " + string(out.Operator)
	if out.Reason != "" {
		line += "  " + out.Reason
	}
	fmt.Printf("%s\n  armed as %s, to %s, until %s\n", line, out.ID, out.DeliverTo, out.ExpiresAt.Format("2006-01-02 15:04"))
	return nil
}
