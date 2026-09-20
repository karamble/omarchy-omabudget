// Package api serves the daemon's loopback HTTP API and mounts MCP under it.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/karamble/omarchy-omabudget/alerts"
	"github.com/karamble/omarchy-omabudget/config"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/mcpserver"
)

// maxBody caps a request body. Every request this API accepts is a handful of
// fields, so the cap sits far above real use and far below anything that could
// exhaust memory.
const maxBody = 64 << 10

// Server holds the daemon's state and answers the panel, the CLI and MCP.
type Server struct {
	mu     sync.RWMutex
	cfg    *config.Config
	engine *alerts.Engine
	ledger *domain.Ledger

	logger  *slog.Logger
	version string
	started time.Time
}

func NewServer(cfg *config.Config, logger *slog.Logger, version string) *Server {
	return &Server{
		cfg:     cfg,
		logger:  logger,
		version: version,
		started: time.Now(),
	}
}

func (s *Server) Config() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Server) SetEngine(e *alerts.Engine) {
	s.mu.Lock()
	s.engine = e
	s.mu.Unlock()
}

func (s *Server) SetLedger(l *domain.Ledger) {
	s.mu.Lock()
	s.ledger = l
	s.mu.Unlock()
}

func (s *Server) Ledger() *domain.Ledger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ledger
}

// alertEngine is read lazily: Handler runs before SetEngine, so capturing the
// value at that point would hand MCP a nil engine for the life of the daemon.
func (s *Server) alertEngine() mcpserver.Alerts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.engine == nil {
		return nil
	}
	return s.engine
}

// Snapshot is the alert-facing view. Until the ledger lands it carries only
// the switch; the daemon fills the maps as domain code arrives.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/dashboard", s.handleDashboard)
	mux.HandleFunc("GET /api/accounts", s.handleAccounts)
	mux.HandleFunc("POST /api/accounts", s.handleAddAccount)
	mux.HandleFunc("PUT /api/accounts/{id}", s.handleUpdateAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.handleRemoveAccount)
	mux.HandleFunc("GET /api/reconcile", s.handleReconcile)
	mux.HandleFunc("POST /api/reconcile", s.handleFinishReconcile)
	mux.HandleFunc("GET /api/categories", s.handleCategories)
	mux.HandleFunc("GET /api/budgets", s.handleBudgets)
	mux.HandleFunc("PUT /api/budgets", s.handleSetBudget)
	mux.HandleFunc("POST /api/budgets/helpers", s.handleBudgetHelpers)
	mux.HandleFunc("POST /api/budgets/rollover", s.handleRollover)
	mux.HandleFunc("GET /api/envelopes", s.handleEnvelopes)
	mux.HandleFunc("POST /api/categories", s.handleAddCategory)
	mux.HandleFunc("PUT /api/categories/{id...}", s.handleUpdateCategory)
	mux.HandleFunc("DELETE /api/categories/{id...}", s.handleRemoveCategory)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("GET /api/payees", s.handlePayees)
	mux.HandleFunc("PUT /api/payees/{id}", s.handleUpdatePayee)
	mux.HandleFunc("DELETE /api/payees/{id}", s.handleRemovePayee)
	mux.HandleFunc("GET /api/rates", s.handleRates)
	mux.HandleFunc("PUT /api/rates", s.handleSetRate)
	mux.HandleFunc("DELETE /api/rates/{currency}/{date}", s.handleRemoveRate)
	mux.HandleFunc("GET /api/reports/spending", s.handleSpending)
	mux.HandleFunc("GET /api/reports/metrics", s.handleMetrics)
	mux.HandleFunc("POST /api/export", s.handleExport)
	mux.HandleFunc("POST /api/backup", s.handleBackup)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("GET /api/rules", s.handleRules)
	mux.HandleFunc("POST /api/rules", s.handleAddRule)
	mux.HandleFunc("PUT /api/rules/{id}", s.handleUpdateRule)
	mux.HandleFunc("DELETE /api/rules/{id}", s.handleRemoveRule)
	mux.HandleFunc("POST /api/rules/{id}/post", s.handlePostRule)
	mux.HandleFunc("POST /api/rules/{id}/skip", s.handleSkipRule)
	mux.HandleFunc("GET /api/bills", s.handleBills)
	mux.HandleFunc("GET /api/transactions", s.handleTransactions)
	mux.HandleFunc("POST /api/transactions", s.handleAddTransaction)
	mux.HandleFunc("GET /api/transactions/{id}", s.handleGetTransaction)
	mux.HandleFunc("PUT /api/transactions/{id}", s.handleUpdateTransaction)
	mux.HandleFunc("DELETE /api/transactions/{id}", s.handleDeleteTransaction)
	mux.HandleFunc("POST /api/transactions/{id}/restore", s.handleRestoreTransaction)
	mux.HandleFunc("GET /api/catalogue", s.handleCatalogue)
	mux.HandleFunc("GET /api/alerts", s.handleAlerts)
	mux.HandleFunc("POST /api/alerts", s.handleArm)
	mux.HandleFunc("PUT /api/alerts/{id}", s.handleEditAlert)
	mux.HandleFunc("DELETE /api/alerts/{id}", s.handleDisarm)
	mux.HandleFunc("POST /api/monitoring", s.handleMonitoring)
	mux.HandleFunc("POST /api/mcp", s.handleMCPToggle)
	mux.HandleFunc("POST /api/token/recycle", s.handleRecycleToken)

	// The MCP endpoint is checked per request rather than mounted once, so the
	// switch takes effect immediately instead of at the next daemon restart.
	mcpHandler := mcpserver.Handler(mcpserver.Source{
		State:          s.Snapshot,
		Monitoring:     func() bool { return s.Config().MonitoringOn() },
		Alerts:         s.alertEngine,
		Version:        s.version,
		Dashboard:      s.mcpDashboard,
		Transactions:   s.mcpTransactions,
		AddTransaction: s.mcpAddTransaction,
		Budget:         s.mcpBudget,
		Bills:          s.mcpBills,
		Spending:       s.mcpSpending,
	})
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Config().MCPOn() {
			writeJSON(w, s.logger, http.StatusNotFound, map[string]string{
				"error": "the mcp endpoint is disabled in OMABUDGET settings",
			})
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	return s.withRequestLog(s.withAuth(mux))
}

type healthResponse struct {
	Status       string `json:"status"`
	Version      string `json:"version"`
	Uptime       string `json:"uptime"`
	BaseCurrency string `json:"baseCurrency"`
	// RateReference is what the ledger keeps its figures in and quotes
	// every rate against; empty until the ledger is open.
	RateReference string `json:"rateReference,omitempty"`
	Model         string `json:"model"`
	Monitoring    bool   `json:"monitoring"`
	MCPEnabled    bool   `json:"mcpEnabled"`
	Armed         int    `json:"armed"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	armed := 0
	if e := s.alertEngine(); e != nil {
		armed = len(e.List())
	}
	reference := ""
	if l := s.Ledger(); l != nil {
		reference = l.Reference()
	}
	writeJSON(w, s.logger, http.StatusOK, healthResponse{
		Status:        "ok",
		Version:       s.version,
		Uptime:        time.Since(s.started).Round(time.Second).String(),
		BaseCurrency:  cfg.BaseCurrency,
		RateReference: reference,
		Model:         string(cfg.Model),
		Monitoring:    cfg.MonitoringOn(),
		MCPEnabled:    cfg.MCPOn(),
		Armed:         armed,
	})
}

func (s *Server) handleCatalogue(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, alerts.Catalogue())
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	e := s.alertEngine()
	if e == nil {
		writeJSON(w, s.logger, http.StatusServiceUnavailable, map[string]string{"error": "alerts are not running"})
		return
	}
	writeJSON(w, s.logger, http.StatusOK, e.List())
}

func (s *Server) handleDisarm(w http.ResponseWriter, r *http.Request) {
	e := s.alertEngine()
	if e == nil {
		writeJSON(w, s.logger, http.StatusServiceUnavailable, map[string]string{"error": "alerts are not running"})
		return
	}
	ok, err := e.Disarm(r.PathValue("id"))
	if err != nil {
		writeJSON(w, s.logger, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, s.logger, http.StatusNotFound, map[string]string{"error": "no such trigger"})
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]bool{"disarmed": true})
}

type toggleIn struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleMonitoring(w http.ResponseWriter, r *http.Request) {
	var in toggleIn
	if !s.decode(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		writeJSON(w, s.logger, http.StatusBadRequest, map[string]string{"error": "enabled is required"})
		return
	}
	if err := s.mutate(func(c *config.Config) error { c.Monitoring = in.Enabled; return nil }); err != nil {
		writeJSON(w, s.logger, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]bool{"monitoring": *in.Enabled})
}

func (s *Server) handleMCPToggle(w http.ResponseWriter, r *http.Request) {
	var in toggleIn
	if !s.decode(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		writeJSON(w, s.logger, http.StatusBadRequest, map[string]string{"error": "enabled is required"})
		return
	}
	if err := s.mutate(func(c *config.Config) error { c.MCPEnabled = in.Enabled; return nil }); err != nil {
		writeJSON(w, s.logger, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]bool{"mcpEnabled": *in.Enabled})
}

func (s *Server) handleRecycleToken(w http.ResponseWriter, r *http.Request) {
	tok, err := config.NewAPIToken()
	if err != nil {
		writeJSON(w, s.logger, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.mutate(func(c *config.Config) error { c.APIToken = tok; return nil }); err != nil {
		writeJSON(w, s.logger, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"apiToken": tok})
}

// mutate applies a change to the config under the lock and persists it.
func (s *Server) mutate(apply func(*config.Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := apply(s.cfg); err != nil {
		return err
	}
	return s.cfg.Save()
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := []byte(s.Config().APIToken)
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="omabudget"`)
			writeJSON(w, s.logger, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Debug("request", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
	})
}

// decode reads a JSON body under a hard size cap, reporting the error itself.
// An oversized body answers 413; anything else malformed is a 400.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, s.logger, http.StatusRequestEntityTooLarge,
				map[string]string{"error": "request body too large"})
			return false
		}
		writeJSON(w, s.logger, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("encoding response", "err", err)
	}
}
