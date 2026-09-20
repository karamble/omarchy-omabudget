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
)

// rate keeps the exchange rates a foreign-currency entry is read at, spec 8.
// Nothing fetches them: this app opens no outward connection.
//
//	omabudget rate
//	omabudget rate set USD 0.92
//	omabudget rate set USD 0.94 -date 2026-10-01
//	omabudget rate remove USD 2026-10-01
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
