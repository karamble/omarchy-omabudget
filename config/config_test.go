package config

import (
	"os"
	"path/filepath"
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
