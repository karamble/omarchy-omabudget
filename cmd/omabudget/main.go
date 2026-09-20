// Command omabudget is the CLI: it talks to the daemon and carries the setup
// verbs that change configuration.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/client"
	"github.com/karamble/omarchy-omabudget/config"
)

var version = "dev"

const usage = `OMABUDGET is local-first personal finance: accounts, a ledger, budgets and bills.

usage: omabudget <command> [flags]

entering:
  add <amount> [category]   log an expense; -income, -transfer-to for the rest
                      -split "<category>=<amount>" twice or more splits it across categories
  delete <id>...            soft delete, undoable for 30 days
  undo <id>...              bring a deleted transaction back

looking:
  dashboard           this period at a glance
  accounts            every account with its balance
  transactions        the ledger, newest first; -q, -tag, -payee, -min, -max, -kind, -account, -from, -to
  show <id>           one transaction with its lines and tags
  edit <id>...        change them; -amount, -category, -desc, -date, -account,
                      -tag replaces every tag, -add-tag keeps the ones there
  categories          the category tree; -all includes archived ones
  health              whether the daemon is awake and how it is configured

planning:
  budget              this period's plan against what was spent; -period YYYY-MM
  budget set <category> <amount>
  budget copy [from]  copy a period's plan; average [3|6|12] [category]; median [12] [category]; scale <percent>
  budget rollover     close the previous period into this one under the envelope model
  envelopes           the period's pots: rolled in, assigned, spent, available; -period
  category add <name> create one; -parent, -kind, -icon, -behaviour, -exclude
  category edit <category>
                      -name, -icon, -behaviour, -goal, -goal-due, -exclude=bool, -archived=bool, -sort
  category archive|restore|remove <category>
  category behaviour <category> monthly|rollover|goal|untracked
  category goal <category> <target> <YYYY-MM>
  settings            show; settings base-currency <CODE>, model limits|envelope, period-start <day>, large-amount <amount>
                      rate-source <id> [-url URL] chooses where rate fetch reads
  rate                exchange rates on file; rate set <currency> <rate> [-date], rate remove <currency> <date>
  rate fetch          read the chosen source once and file what it quotes; -source, -url for this press only
  rate accept <currency> <rate> -date D -source S
                      file a rate the fetch held
  rate sources        where a fetch can read from, and who answers
  payees              who money went to; payee rename|category|alias|unalias|merge|remove

  report spending     categories ranked against the period before; -period, or -from and -to
  report metrics      average per day, projected period end, runway, fixed share
  export -o PATH      the ledger as a plain-text journal; -format csv for rows
  backup -o PATH      a consistent copy of the database
                      plan a category for the period; 0 removes the line

accounts:
  add-account <name>  create one; -type, -currency, -opening
  edit-account <name> -name, -institution, -low-balance, -active=false
  remove-account <name>
                      delete one nothing points at; one with history is closed
  reconcile <name> <closing balance>
                      hold a statement against the ledger; -through, -finish

  bills               what is due: overdue first, then the next 30 days
  bill add <name> <amount> [category]
                      a recurring rule; -every, -start, -day last, -auto, -transfer-to
  bill edit|post|skip|remove <id>
  bill list           every rule; -all includes paused ones

alerts:
  catalogue           every path a watch can be armed on
  alerts              list armed watches
  arm <path> <operator>
                      watch a catalogue path; -above, -below, -value, -older-than, -where, -expires-in
  alert edit <id>     change an armed watch's terms
  disarm <id>         take one down

setup:
  monitoring on|off   the master switch for alert evaluation
  mcp-endpoint on|off whether the MCP endpoint answers
  mcp                 print the MCP entry to paste into an agent's config
  recycle             mint a new API token, locking out old clients
  purge               delete the configuration directory and everything in it
  version             print the version

Run "omabudget <command> -h" for the flags of one command.
`

type common struct {
	addr   string
	config string
	json   bool
}

func bind(fs *flag.FlagSet) *common {
	c := &common{}
	fs.StringVar(&c.addr, "addr", "127.0.0.1:8097", "daemon address")
	fs.StringVar(&c.config, "config", "", "path to config.json")
	fs.BoolVar(&c.json, "json", false, "emit raw JSON")
	return c
}

func (c *common) dial() (*client.Client, error) { return client.New(c.addr, c.config) }

// parseMixed lets flags follow positionals, "add 4.50 coffee -desc X", which
// is how the usage reads and how people type. The flag package stops at the
// first positional, so parse repeatedly, peeling one positional each time.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// timeout is how long any one command waits on the daemon.
func timeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "add":
		err = runAdd(args)
	case "show":
		err = runShow(args)
	case "edit":
		err = runEdit(args)
	case "edit-account":
		err = runEditAccount(args)
	case "add-account":
		err = runAddAccount(args)
	case "remove-account":
		err = runRemoveAccount(args)
	case "reconcile":
		err = runReconcile(args)
	case "accounts":
		err = runAccounts(args)
	case "categories":
		err = runCategories(args)
	case "transactions", "ledger":
		err = runTransactions(args)
	case "delete":
		err = runDelete(args)
	case "undo":
		err = runUndo(args)
	case "dashboard":
		err = runDashboard(args)
	case "bills":
		err = runBills(args)
	case "bill":
		err = runBill(args)
	case "budget":
		err = runBudget(args)
	case "report":
		err = runReport(args)
	case "export":
		err = runExport(args)
	case "backup":
		err = runBackup(args)
	case "envelopes":
		err = runEnvelopes(args)
	case "category":
		err = runCategory(args)
	case "payees":
		err = runPayees(args)
	case "payee":
		err = runPayee(args)
	case "rate", "rates":
		err = runRate(args)
	case "settings":
		err = runSettings(args)
	case "health":
		err = runGet(args, "/api/health")
	case "catalogue":
		err = runGet(args, "/api/catalogue")
	case "alerts":
		err = runGet(args, "/api/alerts")
	case "arm":
		err = runArm(args)
	case "alert":
		err = runAlert(args)
	case "disarm":
		err = runDisarm(args)
	case "monitoring":
		err = runToggle(args, "/api/monitoring", "monitoring")
	case "mcp-endpoint":
		err = runToggle(args, "/api/mcp", "mcp-endpoint")
	case "mcp":
		err = runMCP(args)
	case "recycle":
		err = runRecycle(args)
	case "purge":
		err = runPurge(args)
	case "version", "-version", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "omabudget: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "omabudget:", err)
		os.Exit(1)
	}
}

// runGet fetches one JSON document and prints it.
func runGet(args []string, path string) error {
	fs := flag.NewFlagSet(strings.TrimPrefix(path, "/api/"), flag.ExitOnError)
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
	var out any
	if err := cl.Do(ctx, "GET", path, nil, &out); err != nil {
		return err
	}
	return client.PrintJSON(out)
}

func runDisarm(args []string) error {
	fs := flag.NewFlagSet("disarm", flag.ExitOnError)
	c := bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: omabudget disarm <id>")
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out struct {
		Disarmed bool `json:"disarmed"`
	}
	if err := cl.Do(ctx, "DELETE", "/api/alerts/"+fs.Arg(0), nil, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	fmt.Println("disarmed", fs.Arg(0))
	return nil
}

func runToggle(args []string, path, name string) error {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	c := bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	var enabled bool
	switch fs.Arg(0) {
	case "on":
		enabled = true
	case "off":
		enabled = false
	default:
		return fmt.Errorf("usage: omabudget %s on|off", name)
	}
	cl, err := c.dial()
	if err != nil {
		return err
	}
	ctx, cancel := timeout()
	defer cancel()
	var out map[string]bool
	if err := cl.Do(ctx, "POST", path, map[string]bool{"enabled": enabled}, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(out)
	}
	state := "off"
	if enabled {
		state = "on"
	}
	fmt.Printf("%s is %s\n", name, state)
	return nil
}

func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	c := bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := c.config
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(struct {
			URL      string `json:"url"`
			APIToken string `json:"apiToken"`
		}{"http://" + c.addr + "/mcp", cfg.APIToken})
	}
	fmt.Printf(`Add this to the "mcpServers" object of your agent's config:

  "omabudget": {
    "type": "http",
    "url": "http://%s/mcp",
    "headers": { "Authorization": "Bearer %s" }
  }

The token is read from %s.
The endpoint answers only while "omabudget mcp-endpoint on" is set.
Recycling the token with "omabudget recycle" invalidates this entry immediately.
`, c.addr, cfg.APIToken, path)
	return nil
}

func runRecycle(args []string) error {
	fs := flag.NewFlagSet("recycle", flag.ExitOnError)
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
	var out struct {
		Token string `json:"apiToken"`
	}
	if err := cl.Do(ctx, "POST", "/api/token/recycle", nil, &out); err != nil {
		return err
	}
	if c.json {
		return client.PrintJSON(struct {
			APIToken string `json:"apiToken"`
			URL      string `json:"url"`
			Header   string `json:"authorization"`
		}{out.Token, "http://" + c.addr + "/mcp", "Bearer " + out.Token})
	}
	fmt.Println("A new API token is in place. Every client holding the old one is locked out now.")
	fmt.Println()
	fmt.Printf(`Paste this into the "mcpServers" object of your agent's config:

  "omabudget": {
    "type": "http",
    "url": "http://%s/mcp",
    "headers": { "Authorization": "Bearer %s" }
  }

It is shown once, here, because this is the moment it has to be copied.
`, c.addr, out.Token)
	return nil
}

// runPurge deletes the configuration directory. Run it before removing the
// plugin: this binary lives in the folder that removal deletes.
func runPurge(args []string) error {
	fs := flag.NewFlagSet("purge", flag.ExitOnError)
	yes := fs.Bool("yes", false, "do not ask")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := config.Dir()
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("nothing to purge: %s does not exist\n", dir)
		return nil
	}

	fmt.Printf("this deletes %s and everything in it, your ledger included:\n", dir)
	for _, f := range []string{"config.json", "ledger.db", "triggers.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			fmt.Printf("  %s\n", f)
		}
	}
	if !*yes && !confirm("delete it?") {
		fmt.Println("left alone")
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Printf("deleted %s\n", dir)
	fmt.Println("the plugin itself is removed with: omarchy plugin remove karamble.omabudget")
	return nil
}

func confirm(question string) bool {
	fmt.Printf("%s [y/N] ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
