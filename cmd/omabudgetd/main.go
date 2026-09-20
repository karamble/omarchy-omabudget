// Command omabudgetd is the OMABUDGET daemon: it owns the ledger and serves
// the panel, the CLI and MCP over loopback. The shell runs it through the
// plugin's service entry point.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/karamble/omarchy-omabudget/alerts"
	"github.com/karamble/omarchy-omabudget/api"
	"github.com/karamble/omarchy-omabudget/config"
	"github.com/karamble/omarchy-omabudget/db"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "omabudgetd:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8097", "listen address; loopback by design")
	configPath := flag.String("config", "", "path to config.json (default ~/.config/omabudget/config.json)")
	debug := flag.Bool("debug", false, "log every request")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Println("omabudgetd", version)
		return nil
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	path := *configPath
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if errors.Is(err, config.ErrNotConfigured) {
		cfg = &config.Config{}
		cfg.SetPath(path)
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("creating %s: %w", path, err)
		}
		logger.Info("created a fresh configuration", "path", path)
	} else if err != nil {
		return err
	}

	// Clear temporary files left by a write that was interrupted.
	if d, err := store.Shared(config.Dir()); err == nil {
		if n, err := d.Sweep(".triggers-", ".config-"); err == nil && n > 0 {
			logger.Info("swept interrupted writes", "files", n)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := api.NewServer(cfg, logger, version)

	ledgerDB, err := db.Open(ctx, config.DatabasePath(), db.Options{RateReference: cfg.BaseCurrency})
	if err != nil {
		return fmt.Errorf("opening the ledger: %w", err)
	}
	defer ledgerDB.Close()
	ledger := domain.New(ledgerDB)
	srv.SetLedger(ledger)
	if v, err := ledgerDB.Version(ctx); err == nil {
		logger.Info("ledger open", "path", ledgerDB.Path(), "schema", v)
	}
	go postDueBills(ctx, ledger, logger)
	go sweepRecycleBin(ctx, ledger, logger)

	triggers, err := alerts.Load(config.TriggersPath())
	if err != nil {
		return fmt.Errorf("loading triggers: %w", err)
	}
	engine := alerts.NewEngine(
		triggers,
		srv.Snapshot,
		func() bool { return srv.Config().MonitoringOn() },
		alerts.NewDeliverer(alerts.Desktop),
		logger,
	)
	srv.SetEngine(engine)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", *addr, err)
	}
	logger.Info("omabudgetd listening",
		"addr", ln.Addr().String(), "version", version, "config", config.Dir(),
		"model", cfg.Model, "currency", cfg.BaseCurrency)

	// Deadlines on every phase, not just the header: a local client holding a
	// connection open, or trickling a body, must not pin the daemon.
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go engine.Run(ctx)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Exit zero: the service entry point must not respawn a stop that was
	// asked for.
	logger.Info("omabudgetd stopped cleanly")
	return nil
}

// postDueBills writes the auto-posting rules' due occurrences at start and
// then once an hour, so a bill falls on its day whether or not the app is
// open.
func postDueBills(ctx context.Context, ledger *domain.Ledger, logger *slog.Logger) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		n, err := ledger.PostDue(ctx, time.Now().Format("2006-01-02"))
		if err != nil {
			logger.Warn("posting due bills", "err", err)
		} else if n > 0 {
			logger.Info("posted due bills", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// sweepRecycleBin drops transactions deleted more than thirty days ago, at
// start and then daily.
func sweepRecycleBin(ctx context.Context, ledger *domain.Ledger, logger *slog.Logger) {
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		before := time.Now().AddDate(0, 0, -30).UTC().Format(time.RFC3339)
		if n, err := ledger.SweepDeleted(ctx, before); err != nil {
			logger.Warn("sweeping the recycle bin", "err", err)
		} else if n > 0 {
			logger.Info("swept the recycle bin", "removed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
