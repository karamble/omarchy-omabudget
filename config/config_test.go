package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLargeAmountCurrencyBackfill: a version 1 config gets the currency its
// large amount was typed in, which was the base then, once; a version 2
// config keeps its own record whatever the base says now.
func TestLargeAmountCurrencyBackfill(t *testing.T) {
	load := func(raw string) *Config {
		t.Helper()
		path := filepath.Join(t.TempDir(), fileName)
		if err := os.WriteFile(path, []byte(raw), perm); err != nil {
			t.Fatal(err)
		}
		c, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	old := load(`{"version":1,"apiToken":"x","baseCurrency":"EUR","largeAmount":50000}`)
	if old.Version != 2 || old.LargeAmountCurrency != "EUR" {
		t.Errorf("version 1 loaded as version %d with large amount in %q", old.Version, old.LargeAmountCurrency)
	}
	moved := load(`{"version":2,"apiToken":"x","baseCurrency":"PLN","largeAmount":50000,"largeAmountCurrency":"EUR"}`)
	if moved.LargeAmountCurrency != "EUR" {
		t.Errorf("version 2 rewrote the large amount's currency to %q", moved.LargeAmountCurrency)
	}
	fresh := &Config{}
	fresh.defaults()
	if fresh.Version != 2 || fresh.LargeAmountCurrency != "EUR" {
		t.Errorf("fresh config = version %d, large amount in %q", fresh.Version, fresh.LargeAmountCurrency)
	}
}

// TestRateSourceStaysEmptyByDefault: the source is chosen at use, not
// written into the file, so a config saved before a source existed and one
// saved with none chosen both read the same, and a choice round-trips.
func TestRateSourceStaysEmptyByDefault(t *testing.T) {
	fresh := &Config{}
	fresh.defaults()
	if fresh.RateSource != "" || fresh.RateSourceURL != "" {
		t.Errorf("defaults chose a source: %q %q", fresh.RateSource, fresh.RateSourceURL)
	}
	path := filepath.Join(t.TempDir(), fileName)
	fresh.SetPath(path)
	if err := fresh.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "rateSource") {
		t.Errorf("an unchosen source was written: %s", raw)
	}
	fresh.RateSource, fresh.RateSourceURL = "frankfurter", "http://127.0.0.1:8080/v1/latest"
	if err := fresh.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.RateSource != "frankfurter" || back.RateSourceURL != "http://127.0.0.1:8080/v1/latest" {
		t.Errorf("loaded %q %q", back.RateSource, back.RateSourceURL)
	}
}
