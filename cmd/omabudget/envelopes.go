package main

import (
	"context"
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

// envelopes shows the period under the envelope model: what rolled in, what
// was assigned, what is left, and what is not in any pot yet.
func runEnvelopes(args []string) error {
	fs := flag.NewFlagSet("envelopes", flag.ExitOnError)
	c := bind(fs)
	period := fs.String("period", "", "YYYY-MM (default: the current period)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	path := "/api/envelopes"
	if *period != "" {
		path += "?" + url.Values{"period": {*period}}.Encode()
	}
	var e domain.Envelopes
	if err := cl.Do(ctx, "GET", path, nil, &e); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(e)
	}
	cur, err := baseCurrency(ctx, cl)
	if err != nil {
		return err
	}
	f := func(v int64) string { return money.New(v, cur).Format() }
	fmt.Printf("period %s  liquid %s  in pots %s  to be budgeted %s %s", e.Period, f(e.Liquid), f(e.Held), f(e.ToBeBudgeted), cur)
	if e.Deficit > 0 {
		fmt.Printf("  overspent %s", f(e.Deficit))
	}
	fmt.Println()
	if len(e.Items) == 0 {
		fmt.Println("no pots yet: omabudget budget set <category> <amount>")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CATEGORY\tROLLED IN\tASSIGNED\tSPENT\tAVAILABLE\tPOT\tGOAL")
	for _, it := range e.Items {
		goal := ""
		if it.GoalTarget > 0 {
			goal = fmt.Sprintf("%s by %s", f(it.GoalTarget), it.GoalDue)
			if it.Accrual > 0 {
				goal += fmt.Sprintf(", %s per period", f(it.Accrual))
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", it.Name, f(it.RolloverIn), f(it.Assigned), f(it.Spent), f(it.Available), it.Behaviour, goal)
	}
	return tw.Flush()
}

// category manages the taxonomy: the groups and their children, spec 1.4.
//
//	omabudget category add "Side projects" -kind income
//	omabudget category add "Plugin sales" -parent "Side projects"
//	omabudget category edit Groceries -name "Food shopping" -exclude=false
//	omabudget category archive "Coffee & snacks"
//	omabudget category remove Spare
//	omabudget category behaviour Fuel rollover
//	omabudget category goal Accommodation 1200 2027-06
func runCategory(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: omabudget category add|edit|archive|restore|remove|behaviour|goal ...")
	}
	switch args[0] {
	case "add":
		return runCategoryAdd(args[1:])
	case "edit":
		return runCategoryEdit(args[1:])
	case "archive":
		return runCategoryArchive(args[1:], true)
	case "restore":
		return runCategoryArchive(args[1:], false)
	case "remove":
		return runCategoryRemove(args[1:])
	case "behaviour", "goal":
		return runCategorySet(args)
	}
	return fmt.Errorf("unknown category verb %q: add, edit, archive, restore, remove, behaviour or goal", args[0])
}

func runCategoryAdd(args []string) error {
	fs := flag.NewFlagSet("category add", flag.ExitOnError)
	c := bind(fs)
	parent := fs.String("parent", "", "the group it belongs under; without one it is a group itself")
	kind := fs.String("kind", "", "expense (default) or income; a child takes its parent's")
	icon := fs.String("icon", "", "a Nerd Font glyph, for groups")
	behaviour := fs.String("behaviour", "", "monthly (default), rollover or untracked")
	exclude := fs.Bool("exclude", false, "keep it out of the statistics")
	sort := fs.Int("sort", 0, "position among its siblings, lowest first")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: omabudget category add <name> [-parent <group>] [-kind expense|income] [-icon <glyph>]")
	}
	in := map[string]any{
		"name": strings.Join(pos, " "), "parent": *parent, "kind": *kind, "icon": *icon,
		"behaviour": *behaviour, "excludedFromStatistics": *exclude, "sortOrder": *sort,
	}
	return categoryDo(c, "POST", "/api/categories", in)
}

func runCategoryEdit(args []string) error {
	fs := flag.NewFlagSet("category edit", flag.ExitOnError)
	c := bind(fs)
	name := fs.String("name", "", "a new name; the id and the history stay")
	icon := fs.String("icon", "", "a Nerd Font glyph; empty clears it")
	behaviour := fs.String("behaviour", "", "monthly, rollover, goal or untracked")
	goal := fs.String("goal", "", "save toward this target; 0 clears the goal")
	goalDue := fs.String("goal-due", "", "the month the goal is due, YYYY-MM")
	exclude := fs.Bool("exclude", false, "-exclude=false counts it in the statistics again")
	archived := fs.Bool("archived", false, "-archived=true takes it out of the pickers")
	sort := fs.Int("sort", 0, "position among its siblings")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: omabudget category edit <category> [-name] [-icon] [-behaviour] [-exclude=bool] [-archived=bool] [-sort N]")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if len(set) == 0 {
		return errors.New("nothing to change: give at least one flag")
	}
	in := map[string]any{}
	if set["name"] {
		in["name"] = *name
	}
	if set["icon"] {
		in["icon"] = *icon
	}
	if set["behaviour"] {
		in["behaviour"] = *behaviour
	}
	if set["goal"] {
		in["goalTarget"] = *goal
		in["goalDue"] = *goalDue
	}
	if set["exclude"] {
		in["excludedFromStatistics"] = *exclude
	}
	if set["archived"] {
		in["archived"] = *archived
	}
	if set["sort"] {
		in["sortOrder"] = *sort
	}
	return categoryPut(c, strings.Join(pos, " "), in)
}

func runCategoryArchive(args []string, archived bool) error {
	verb := "archive"
	if !archived {
		verb = "restore"
	}
	fs := flag.NewFlagSet("category "+verb, flag.ExitOnError)
	c := bind(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("usage: omabudget category %s <category>", verb)
	}
	return categoryPut(c, strings.Join(pos, " "), map[string]any{"archived": archived})
}

func runCategoryRemove(args []string) error {
	fs := flag.NewFlagSet("category remove", flag.ExitOnError)
	c := bind(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: omabudget category remove <category>")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	ref := strings.Join(pos, " ")
	id, err := categoryID(ctx, cl, ref)
	if err != nil {
		return err
	}
	var out map[string]string
	if err := cl.Do(ctx, "DELETE", "/api/categories/"+url.PathEscape(id), nil, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	fmt.Printf("removed %s\n", ref)
	return nil
}

// category behaviour <category> monthly|rollover|goal|untracked
// category goal <category> <target> <YYYY-MM>, or a target of 0 to clear
func runCategorySet(args []string) error {
	fs := flag.NewFlagSet("category", flag.ExitOnError)
	c := bind(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 3 {
		return errors.New("usage: omabudget category behaviour <category> <monthly|rollover|goal|untracked>, or: category goal <category> <target> [YYYY-MM]")
	}
	in := map[string]any{}
	var ref string
	switch pos[0] {
	case "behaviour":
		ref = strings.Join(pos[1:len(pos)-1], " ")
		in["behaviour"] = pos[len(pos)-1]
	case "goal":
		rest := pos[1:]
		due := ""
		if len(rest) >= 3 && len(rest[len(rest)-1]) == 7 && rest[len(rest)-1][4] == '-' {
			due = rest[len(rest)-1]
			rest = rest[:len(rest)-1]
		}
		if len(rest) < 2 {
			return errors.New("usage: omabudget category goal <category> <target> [YYYY-MM]")
		}
		ref = strings.Join(rest[:len(rest)-1], " ")
		in["goalTarget"] = rest[len(rest)-1]
		in["goalDue"] = due
	}
	return categoryPut(c, ref, in)
}

// categoryID resolves a reference to an id the way the daemon does, falling
// back to the reference itself so the daemon reports what it could not find.
func categoryID(ctx context.Context, cl *client.Client, ref string) (string, error) {
	var cats []domain.Category
	if err := cl.Do(ctx, "GET", "/api/categories", nil, &cats); err != nil {
		return "", err
	}
	for _, cat := range cats {
		if cat.ID == ref {
			return cat.ID, nil
		}
	}
	path := func(cat domain.Category) string {
		if cat.ParentName != "" {
			return cat.ParentName + " / " + cat.Name
		}
		return cat.Name
	}
	for _, cat := range cats {
		if strings.EqualFold(cat.Name, ref) || strings.EqualFold(path(cat), ref) {
			return cat.ID, nil
		}
	}
	return ref, nil
}

func categoryPut(c *common, ref string, in map[string]any) error {
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	id, err := categoryID(ctx, cl, ref)
	if err != nil {
		return err
	}
	return categoryReport(c, cl, ctx, "PUT", "/api/categories/"+url.PathEscape(id), in)
}

func categoryDo(c *common, method, path string, in map[string]any) error {
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	return categoryReport(c, cl, ctx, method, path, in)
}

func categoryReport(c *common, cl *client.Client, ctx context.Context, method, path string, in map[string]any) error {
	var out domain.Category
	if err := cl.Do(ctx, method, path, in, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	line := out.Name
	if out.ParentName != "" {
		line = out.ParentName + " / " + out.Name
	}
	line += "  " + out.Kind
	if out.Icon != "" {
		line += "  " + out.Icon
	}
	line += "  " + out.Behaviour
	if out.GoalTarget > 0 {
		cur, _ := baseCurrency(ctx, cl)
		line += "  goal " + money.New(out.GoalTarget, cur).Format() + " by " + out.GoalDue
	}
	if out.Excluded {
		line += "  not in statistics"
	}
	if out.Archived {
		line += "  archived"
	}
	fmt.Println(line)
	return nil
}

// settings shows or changes what the app keeps in its configuration.
//
//	omabudget settings
//	omabudget settings base-currency PLN
//	omabudget settings model envelope
//	omabudget settings period-start 10
//	omabudget settings large-amount 500
//	omabudget settings rate-source frankfurter -url http://192.168.1.20:8080/v1/latest
func runSettings(args []string) error {
	fs := flag.NewFlagSet("settings", flag.ExitOnError)
	c := bind(fs)
	rawURL := fs.String("url", "", "with rate-source: an instance of your own; empty is the source's own endpoint")
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
	var out struct {
		BaseCurrency   string `json:"baseCurrency"`
		RateReference  string `json:"rateReference"`
		Model          string `json:"model"`
		PeriodStartDay int    `json:"periodStartDay"`
		LargeAmount    int64  `json:"largeAmount"`
		Monitoring     bool   `json:"monitoring"`
		RateSource     string `json:"rateSource"`
		RateSourceURL  string `json:"rateSourceUrl"`
		LastFetch      *struct {
			At     string `json:"at"`
			Source string `json:"source"`
			Host   string `json:"host"`
		} `json:"lastFetch"`
	}
	switch {
	case len(pos) == 0:
		err = cl.Do(ctx, "GET", "/api/settings", nil, &out)
	case len(pos) == 2 && pos[0] == "base-currency":
		err = cl.Do(ctx, "PUT", "/api/settings", map[string]any{"baseCurrency": pos[1]}, &out)
	case len(pos) == 2 && pos[0] == "model":
		err = cl.Do(ctx, "PUT", "/api/settings", map[string]any{"model": pos[1]}, &out)
	case len(pos) == 2 && pos[0] == "period-start":
		n, convErr := strconv.Atoi(pos[1])
		if convErr != nil {
			return fmt.Errorf("period-start wants a day of the month, got %q", pos[1])
		}
		err = cl.Do(ctx, "PUT", "/api/settings", map[string]any{"periodStartDay": n}, &out)
	case len(pos) == 2 && pos[0] == "large-amount":
		err = cl.Do(ctx, "PUT", "/api/settings", map[string]any{"largeAmount": pos[1]}, &out)
	case len(pos) == 2 && pos[0] == "rate-source":
		err = cl.Do(ctx, "PUT", "/api/settings", map[string]any{"rateSource": pos[1], "rateSourceUrl": *rawURL}, &out)
	default:
		return errors.New("usage: omabudget settings [base-currency <CODE> | model limits|envelope | period-start <1-28> | large-amount <amount> | rate-source <id> [-url URL]]")
	}
	if err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	fmt.Printf("currency      %s\nrates against %s\nmodel         %s\nperiod start  day %d\nlarge amount  %s\nmonitoring    %v\n",
		out.BaseCurrency, out.RateReference, out.Model, out.PeriodStartDay, money.New(out.LargeAmount, out.BaseCurrency).Format(), out.Monitoring)
	source := out.RateSource
	if out.RateSourceURL != "" {
		source += " at " + out.RateSourceURL
	}
	fmt.Printf("rate source   %s\n", source)
	if out.LastFetch == nil {
		fmt.Println("network       never used")
	} else {
		fmt.Printf("network       last used %s, %s, for exchange rates\n", out.LastFetch.At, out.LastFetch.Host)
	}
	return nil
}
