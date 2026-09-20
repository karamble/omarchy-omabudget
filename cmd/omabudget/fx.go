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

// rate keeps the exchange rates a foreign-currency entry is read at, spec 8.
// A rate is typed, or fetched from the source chosen in settings by rate
// fetch, which is the one time the daemon opens a connection outward and
// happens only when asked.
//
//	omabudget rate
//	omabudget rate set USD 0.92
//	omabudget rate set USD 0.94 -date 2026-10-01
//	omabudget rate remove USD 2026-10-01
//	omabudget rate fetch
//	omabudget rate fetch -source frankfurter -url http://192.168.1.20:8080/v1/latest
//	omabudget rate accept PLN 0.2291738 -date 2026-09-18 -source ecb
//	omabudget rate sources
func runRate(args []string) error {
	if len(args) == 0 {
		return runRateList(args)
	}
	switch args[0] {
	case "set":
		return runRateSet(args[1:])
	case "remove":
		return runRateRemove(args[1:])
	case "list":
		return runRateList(args[1:])
	case "fetch":
		return runRateFetch(args[1:])
	case "accept":
		return runRateAccept(args[1:])
	case "sources":
		return runRateSources(args[1:])
	}
	return runRateList(args)
}

func runRateList(args []string) error {
	fs := flag.NewFlagSet("rate", flag.ExitOnError)
	c := bind(fs)
	currency := fs.String("currency", "", "only this one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	path := "/api/rates"
	if *currency != "" {
		path += "?" + url.Values{"currency": {*currency}}.Encode()
	}
	var list []domain.FXRate
	if err := cl.Do(ctx, "GET", path, nil, &list); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(list)
	}
	if len(list) == 0 {
		fmt.Println("no rates yet: omabudget rate set <currency> <rate>")
		return nil
	}
	_, reference, err := currencies(ctx, cl)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "DATE\tCURRENCY\tRATE\tSOURCE\n")
	for _, r := range list {
		fmt.Fprintf(tw, "%s\t%s\t1 %s = %s %s\t%s\n", r.Date, r.Currency, r.Currency, r.Rate, reference, r.Source)
	}
	return tw.Flush()
}

func runRateSet(args []string) error {
	fs := flag.NewFlagSet("rate set", flag.ExitOnError)
	c := bind(fs)
	date := fs.String("date", "", "YYYY-MM-DD (default: today)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New("usage: omabudget rate set <currency> <rate> [-date YYYY-MM-DD]")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out domain.FXRate
	if err := cl.Do(ctx, "PUT", "/api/rates", map[string]string{
		"currency": pos[0], "rate": pos[1], "date": *date,
	}, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	_, reference, _ := currencies(ctx, cl)
	fmt.Printf("%s  1 %s = %s %s\n", out.Date, out.Currency, out.Rate, reference)
	if out.Rederived > 0 {
		fmt.Printf("re-derived %d transactions\n", out.Rederived)
	}
	return nil
}

func runRateRemove(args []string) error {
	fs := flag.NewFlagSet("rate remove", flag.ExitOnError)
	c := bind(fs)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New("usage: omabudget rate remove <currency> <YYYY-MM-DD>")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out map[string]string
	if err := cl.Do(ctx, "DELETE", "/api/rates/"+url.PathEscape(strings.ToUpper(pos[0]))+"/"+url.PathEscape(pos[1]), nil, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	fmt.Printf("removed the %s rate for %s\n", strings.ToUpper(pos[0]), pos[1])
	return nil
}

// fetchOut mirrors the daemon's answer to a fetch.
type fetchOut struct {
	Source    string                `json:"source"`
	Name      string                `json:"name"`
	Host      string                `json:"host"`
	Published string                `json:"published"`
	Rates     map[string]money.Rate `json:"rates"`
	domain.Applied
}

// runRateFetch asks the daemon to read the chosen source once. The flags
// override the setting for this press only.
func runRateFetch(args []string) error {
	fs := flag.NewFlagSet("rate fetch", flag.ExitOnError)
	c := bind(fs)
	source := fs.String("source", "", "read this source instead of the one in settings; rate sources lists them")
	rawURL := fs.String("url", "", "read an instance of your own, for a source that allows one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out fetchOut
	if err := cl.Do(ctx, "POST", "/api/rates/fetch", map[string]string{"source": *source, "url": *rawURL}, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	_, reference, err := currencies(ctx, cl)
	if err != nil {
		return err
	}
	fmt.Printf("read %s at %s, published %s\n", out.Name, out.Host, out.Published)
	if len(out.Filed)+len(out.Unchanged)+len(out.Kept)+len(out.Held) == 0 {
		fmt.Println("nothing to file: no currency in use is quoted by the source")
	} else {
		fmt.Println()
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "CURRENCY\tSTATUS\tRATE\n")
		quote := func(currency string) string {
			return fmt.Sprintf("1 %s = %s %s", currency, out.Rates[currency], reference)
		}
		for _, currency := range out.Filed {
			fmt.Fprintf(tw, "%s\tfiled\t%s\n", currency, quote(currency))
		}
		for _, currency := range out.Unchanged {
			fmt.Fprintf(tw, "%s\tunchanged\t%s, already on file\n", currency, quote(currency))
		}
		for _, currency := range out.Kept {
			fmt.Fprintf(tw, "%s\tkept\ttyped by hand for %s\n", currency, out.Published)
		}
		for _, h := range out.Held {
			fmt.Fprintf(tw, "%s\theld\t%s\n", h.Currency, h.Reason)
			fmt.Fprintf(tw, "\t\tomabudget rate accept %s %s -date %s -source %s\n", h.Currency, h.Rate, h.Date, out.Source)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(out.Unquoted) > 0 {
		fmt.Printf("\nnot quoted by %s, entered by hand: %s\n", out.Name, strings.Join(out.Unquoted, ", "))
	}
	for _, note := range out.Notes {
		fmt.Println("note:", note)
	}
	if out.Rederived > 0 {
		fmt.Printf("re-derived %d transactions\n", out.Rederived)
	}
	return nil
}

// runRateAccept files one rate a fetch held, as the fetch output spells it.
func runRateAccept(args []string) error {
	fs := flag.NewFlagSet("rate accept", flag.ExitOnError)
	c := bind(fs)
	date := fs.String("date", "", "the date the source published it, YYYY-MM-DD")
	source := fs.String("source", "", "the source that quoted it")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 || *date == "" || *source == "" {
		return errors.New("usage: omabudget rate accept <currency> <rate> -date YYYY-MM-DD -source <id>")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out domain.FXRate
	if err := cl.Do(ctx, "POST", "/api/rates/accept", map[string]string{
		"currency": pos[0], "rate": pos[1], "date": *date, "source": *source,
	}, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	_, reference, _ := currencies(ctx, cl)
	fmt.Printf("%s  1 %s = %s %s  %s\n", out.Date, out.Currency, out.Rate, reference, out.Source)
	if out.Rederived > 0 {
		fmt.Printf("re-derived %d transactions\n", out.Rederived)
	}
	return nil
}

// runRateSources lists where a fetch can read from, and who answers.
func runRateSources(args []string) error {
	fs := flag.NewFlagSet("rate sources", flag.ExitOnError)
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
	var list []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		What   string `json:"what"`
		URL    string `json:"url"`
		Custom bool   `json:"custom"`
	}
	if err := cl.Do(ctx, "GET", "/api/rates/sources", nil, &list); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(list)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\tNAME\tREADS FROM\n")
	for _, src := range list {
		where := src.URL
		if src.Custom {
			where += "  (or -url for one you run)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", src.ID, src.Name, where)
		fmt.Fprintf(tw, "\t\t%s\n", src.What)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Println("\nchoose one: omabudget settings rate-source <id> [-url URL]")
	return nil
}
