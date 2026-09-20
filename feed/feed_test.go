package feed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture parses one captured response through its source.
func fixture(t *testing.T, src Source, name string) Quote {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	q, err := src.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// TestSourcesAgree: the two sources are one dataset over two transports,
// so the captured responses carry the same date, the same currencies and
// the same numbers, differing only in trailing zeros.
func TestSourcesAgree(t *testing.T) {
	ecb := fixture(t, ECB, "ecb-daily.xml")
	ff := fixture(t, Frankfurter, "frankfurter.json")
	if ecb.Base != "EUR" || ff.Base != "EUR" || ecb.Date != "2026-09-18" || ff.Date != ecb.Date {
		t.Fatalf("ecb %s %s, frankfurter %s %s", ecb.Base, ecb.Date, ff.Base, ff.Date)
	}
	if len(ecb.Rates) != 29 || len(ff.Rates) != 29 {
		t.Fatalf("ecb has %d rates, frankfurter %d, want 29", len(ecb.Rates), len(ff.Rates))
	}
	for currency, rate := range ecb.Rates {
		other, ok := ff.Rates[currency]
		if !ok {
			t.Errorf("%s is missing from frankfurter", currency)
			continue
		}
		if !rate.Equal(other) {
			t.Errorf("%s: ecb %s, frankfurter %s", currency, rate, other)
		}
	}
	// The numbers are read as written, not through a float.
	if ecb.Rates["USD"] != "1.1460" || ff.Rates["USD"] != "1.146" || ecb.Rates["GBP"] != "0.85880" {
		t.Errorf("usd %s %s, gbp %s", ecb.Rates["USD"], ff.Rates["USD"], ecb.Rates["GBP"])
	}
	if _, ok := ecb.Rates["EUR"]; ok {
		t.Error("the base is quoted against itself")
	}
}

// TestAgainst: the published direction is 1 base = n currency, and the
// table's is 1 currency = x reference, so every number is inverted, and
// crossed when the reference is not the base.
func TestAgainst(t *testing.T) {
	q := fixture(t, ECB, "ecb-daily.xml")

	eur, err := q.Against("EUR")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := eur["EUR"]; ok {
		t.Error("the reference is quoted")
	}
	// 1/1.1460 is 500/573.
	if eur["USD"] != "0.87260035" || eur["JPY"] != "0.0055266939" || eur["GBP"] != "1.1644155" {
		t.Errorf("usd %s jpy %s gbp %s", eur["USD"], eur["JPY"], eur["GBP"])
	}
	if len(eur) != 29 {
		t.Errorf("%d rates against EUR, want 29", len(eur))
	}

	// Against the dollar: the euro is 1.1460, and the pound crosses as
	// 1.1460/0.85880.
	usd, err := q.Against("USD")
	if err != nil {
		t.Fatal(err)
	}
	if usd["EUR"] != "1.146" || usd["GBP"] != "1.3344201" || usd["JPY"] != "0.0063335912" {
		t.Errorf("eur %s gbp %s jpy %s", usd["EUR"], usd["GBP"], usd["JPY"])
	}
	if _, ok := usd["USD"]; ok {
		t.Error("the reference is quoted")
	}
	if len(usd) != 29 {
		t.Errorf("%d rates against USD, want 29", len(usd))
	}

	// Every filed rate is a decimal a form can read as a number.
	for currency, rate := range usd {
		if strings.Contains(string(rate), "/") {
			t.Errorf("%s is filed as a fraction: %s", currency, rate)
		}
	}
	if _, err := q.Against("XXX"); err == nil {
		t.Error("an unquoted reference was crossed")
	}
}

func TestParseRefusesBadShapes(t *testing.T) {
	cases := []struct {
		name string
		src  Source
		body string
	}{
		{"empty xml", ECB, `<?xml version="1.0"?><Envelope><Cube></Cube></Envelope>`},
		{"undated xml", ECB, `<Envelope><Cube><Cube><Cube currency="USD" rate="1.1"/></Cube></Cube></Envelope>`},
		{"zero rate", ECB, `<Envelope><Cube><Cube time="2026-09-18"><Cube currency="USD" rate="0"/></Cube></Cube></Envelope>`},
		{"lowercase code", ECB, `<Envelope><Cube><Cube time="2026-09-18"><Cube currency="usd" rate="1.1"/></Cube></Cube></Envelope>`},
		{"not a number", ECB, `<Envelope><Cube><Cube time="2026-09-18"><Cube currency="USD" rate="one"/></Cube></Cube></Envelope>`},
		{"html", ECB, `<html><body>301</body></html>`},
		{"no rates", Frankfurter, `{"amount":1.0,"base":"EUR","date":"2026-09-18","rates":{}}`},
		{"per hundred", Frankfurter, `{"amount":100,"base":"EUR","date":"2026-09-18","rates":{"USD":114.6}}`},
		{"negative", Frankfurter, `{"amount":1,"base":"EUR","date":"2026-09-18","rates":{"USD":-1.1}}`},
		{"base quoted", Frankfurter, `{"amount":1,"base":"EUR","date":"2026-09-18","rates":{"EUR":1}}`},
		{"bad date", Frankfurter, `{"amount":1,"base":"EUR","date":"18/09/2026","rates":{"USD":1.1}}`},
		{"no base", Frankfurter, `{"amount":1,"date":"2026-09-18","rates":{"USD":1.1}}`},
		{"html", Frankfurter, `<html><body>301</body></html>`},
	}
	for _, c := range cases {
		if _, err := c.src.Parse(strings.NewReader(c.body)); err == nil {
			t.Errorf("%s %s was accepted", c.src.ID, c.name)
		}
	}
}

func TestSourceRegistry(t *testing.T) {
	for _, s := range Sources {
		if s.ID == "" || s.Name == "" || s.What == "" || s.parse == nil {
			t.Errorf("%+v is incomplete", s)
		}
		if !strings.HasPrefix(s.URL, "https://") {
			t.Errorf("%s is not https: %s", s.ID, s.URL)
		}
		found, ok := Lookup(s.ID)
		if !ok || found.ID != s.ID {
			t.Errorf("Lookup(%s) = %+v, %v", s.ID, found, ok)
		}
	}
	if _, ok := Lookup("bank"); ok {
		t.Error("an unknown source was found")
	}
	// A custom endpoint changes nothing but the URL.
	own := Frankfurter.At("http://10.0.0.5:8080/v1/latest")
	if own.ID != "frankfurter" || own.URL != "http://10.0.0.5:8080/v1/latest" || Frankfurter.URL == own.URL {
		t.Errorf("%+v", own)
	}
	if !Frankfurter.Custom || ECB.Custom {
		t.Error("only frankfurter accepts a url of the user's own")
	}
	// The request asks for everything, not a subset.
	for _, s := range Sources {
		for _, leak := range []string{"base=", "symbols=", "from=", "to="} {
			if strings.Contains(s.URL, leak) {
				t.Errorf("%s asks for a subset: %s", s.ID, s.URL)
			}
		}
	}
}
