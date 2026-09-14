package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/karamble/omarchy-omabudget/client"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/money"
)

// report spending [-period YYYY-MM | -from -to], report metrics
func runReport(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: omabudget report spending [-period YYYY-MM | -from YYYY-MM-DD -to YYYY-MM-DD], or: report metrics")
	}
	switch args[0] {
	case "spending":
		return runReportSpending(args[1:])
	case "metrics":
		return runReportMetrics(args[1:])
	}
	return fmt.Errorf("unknown report %q: spending or metrics", args[0])
}

func runReportSpending(args []string) error {
	fs := flag.NewFlagSet("report spending", flag.ExitOnError)
	c := bind(fs)
	period := fs.String("period", "", "YYYY-MM (default: the current period)")
	from := fs.String("from", "", "YYYY-MM-DD, with -to, instead of a period")
	to := fs.String("to", "", "YYYY-MM-DD")
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
	if *period != "" {
		q.Set("period", *period)
	}
	if *from != "" || *to != "" {
		q.Set("from", *from)
		q.Set("to", *to)
	}
	var rep domain.SpendingReport
	if err := cl.Do(ctx, "GET", "/api/reports/spending?"+q.Encode(), nil, &rep); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(rep)
	}
	cur, err := baseCurrency(ctx, cl)
	if err != nil {
		return err
	}
	f := func(v int64) string { return money.New(v, cur).Format() }
	fmt.Printf("%s to %s  spent %s %s  (before: %s to %s, %s)\n", rep.From, rep.To, f(rep.Total), cur, rep.PrevFrom, rep.PrevTo, f(rep.Previous))
	if len(rep.Rows) == 0 {
		fmt.Println("nothing spent")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CATEGORY\tSPENT\tSHARE\tBEFORE\tCHANGE\tPLANNED")
	for _, row := range rep.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%d%%\t%s\t%s\t%s\n", row.Name, f(row.Spent), row.Share, f(row.Previous), change(row), plannedText(row.Planned, f))
		for _, ch := range row.Children {
			fmt.Fprintf(tw, "    %s\t%s\t%d%%\t%s\t%s\t%s\n", ch.Name, f(ch.Spent), ch.Share, f(ch.Previous), change(ch), plannedText(ch.Planned, f))
		}
	}
	return tw.Flush()
}

func change(row domain.CategorySpend) string {
	if row.DeltaPct == nil {
		if row.Delta == 0 {
			return ""
		}
		return "new"
	}
	return fmt.Sprintf("%+.1f%%", *row.DeltaPct)
}

func plannedText(planned int64, f func(int64) string) string {
	if planned <= 0 {
		return ""
	}
	return f(planned)
}

func runReportMetrics(args []string) error {
	fs := flag.NewFlagSet("report metrics", flag.ExitOnError)
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
	var m domain.Metrics
	if err := cl.Do(ctx, "GET", "/api/reports/metrics", nil, &m); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(m)
	}
	cur, err := baseCurrency(ctx, cl)
	if err != nil {
		return err
	}
	f := func(v int64) string { return money.New(v, cur).Format() }
	fmt.Printf("spent so far          %s %s\n", f(m.Expense), cur)
	fmt.Printf("average per day       %s\n", f(m.AverageDaily))
	fmt.Printf("still due this period %s\n", f(m.RecurringDue))
	fmt.Printf("projected period end  %s\n", f(m.Projected))
	fmt.Printf("trailing period avg   %s\n", f(m.Trailing))
	fmt.Printf("runway                %.1f months of liquid funds\n", m.RunwayMonths)
	fmt.Printf("fixed share           %d%% of spending posted by rules\n", m.FixedShare)
	return nil
}

// export -o PATH [-format journal|csv]; backup -o PATH
func runExport(args []string) error {
	return runWriteFile(args, "export", "/api/export", "omabudget.journal")
}

func runBackup(args []string) error {
	return runWriteFile(args, "backup", "/api/backup", "omabudget-backup.db")
}

func runWriteFile(args []string, name, path, defaultName string) error {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	c := bind(fs)
	out := fs.String("o", "", "where to write, inside your home directory (default: "+defaultName+" in the current directory)")
	format := fs.String("format", "journal", "journal or csv, for export")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := *out
	if target == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		target = wd + "/" + defaultName
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var res struct {
		Path  string `json:"path"`
		Bytes int    `json:"bytes"`
	}
	if err := cl.Do(ctx, "POST", path, map[string]string{"path": target, "format": *format}, &res); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(res)
	}
	fmt.Printf("wrote %s (%d bytes)\n", res.Path, res.Bytes)
	return nil
}
