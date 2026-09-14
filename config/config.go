// Package config holds the daemon's own settings and the bearer token guarding
// its API. The file is written 0600 through the store package.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/karamble/omarchy-omabudget/store"
)

// ErrNotConfigured reports that no config exists yet.
var ErrNotConfigured = errors.New("no configuration: start the daemon once to create one")

// Model is which budgeting model the household uses, spec section 5.
type Model string

const (
	ModelLimits   Model = "limits"
	ModelEnvelope Model = "envelope"
)

// Config is the on-disk configuration. The zero value is usable: Save writes
// it with a freshly generated APIToken if one is missing.
type Config struct {
	Version  int    `json:"version"`
	APIToken string `json:"apiToken"`

	// BaseCurrency is what every statistic is computed in, spec section 8.
	BaseCurrency string `json:"baseCurrency"`

	// Model selects category limits or envelopes.
	Model Model `json:"model"`

	// PeriodStartDay is the day of month a budget period begins, 1 to 28.
	PeriodStartDay int `json:"periodStartDay"`

	// LargeAmount is the threshold, in base minor units, above which a
	// transaction counts as large for alerts. Zero switches the check off.
	LargeAmount int64 `json:"largeAmount"`

	// Monitoring is the master switch for alert evaluation. Nil reads as on.
	Monitoring *bool `json:"monitoring,omitempty"`

	// MCPEnabled exposes the MCP endpoint. Nil reads as off.
	MCPEnabled *bool `json:"mcpEnabled,omitempty"`

	path string
}

const (
	fileName = "config.json"
	perm     = 0o600
)

// Dir is the config directory, one of the two places a plugin may write.
func Dir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "omabudget")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "omabudget")
}

// DefaultPath is where the config lives.
func DefaultPath() string { return filepath.Join(Dir(), fileName) }

// DatabasePath is where the ledger lives.
func DatabasePath() string { return filepath.Join(Dir(), "ledger.db") }

// TriggersPath is where armed alerts live.
func TriggersPath() string { return filepath.Join(Dir(), "triggers.json") }

func (c *Config) MonitoringOn() bool { return c.Monitoring == nil || *c.Monitoring }
func (c *Config) MCPOn() bool        { return c.MCPEnabled != nil && *c.MCPEnabled }

func (c *Config) defaults() {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.BaseCurrency == "" {
		c.BaseCurrency = "EUR"
	}
	if c.Model == "" {
		c.Model = ModelLimits
	}
	if c.PeriodStartDay < 1 || c.PeriodStartDay > 28 {
		c.PeriodStartDay = 1
	}
}

// NewAPIToken mints the bearer token guarding the daemon's API.
func NewAPIToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating api token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Load reads the config, refusing one whose mode is wider than 0600.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	dir, err := store.Shared(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	raw, err := dir.Read(filepath.Base(path), perm)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	c.path = path
	c.defaults()
	return &c, nil
}

// Save writes the config atomically at 0600, minting an APIToken if absent.
func (c *Config) Save() error {
	// A config that was never loaded or pointed at a file has nowhere to go.
	// Defaulting here would let a stray object overwrite the real one.
	if c.path == "" {
		return errors.New("the configuration has no path")
	}
	c.defaults()
	if c.APIToken == "" {
		tok, err := NewAPIToken()
		if err != nil {
			return err
		}
		c.APIToken = tok
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	raw = append(raw, '\n')

	dir, err := store.Shared(filepath.Dir(c.path))
	if err != nil {
		return err
	}
	return dir.Write(filepath.Base(c.path), raw, perm)
}

// SetPath points the config at a file other than the default, for tests.
func (c *Config) SetPath(path string) { c.path = path }

// Path reports where the config lives.
func (c *Config) Path() string {
	if c.path == "" {
		return DefaultPath()
	}
	return c.path
}
