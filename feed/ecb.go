package feed

import (
	"encoding/xml"
	"errors"
	"io"

	"github.com/karamble/omarchy-omabudget/money"
)

// ECB is the central bank's own daily reference rates, quoted in euro.
var ECB = Source{
	ID:    "ecb",
	Name:  "European Central Bank",
	What:  "The official daily reference rates, read from the bank itself. No third party sees the request.",
	URL:   "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml",
	parse: parseECB,
}

// ecbEnvelope is the daily document: one dated cube of currency cubes.
type ecbEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Days    []struct {
		Time  string `xml:"time,attr"`
		Rates []struct {
			Currency string `xml:"currency,attr"`
			Rate     string `xml:"rate,attr"`
		} `xml:"Cube"`
	} `xml:"Cube>Cube"`
}

// parseECB reads the daily XML. The rates are strings in the document and
// stay strings here.
func parseECB(r io.Reader) (Quote, error) {
	var env ecbEnvelope
	if err := xml.NewDecoder(r).Decode(&env); err != nil {
		return Quote{}, err
	}
	if len(env.Days) == 0 {
		return Quote{}, errors.New("no dated rates in the document")
	}
	day := env.Days[0]
	q := Quote{Base: "EUR", Date: day.Time, Rates: map[string]money.Rate{}}
	for _, c := range day.Rates {
		q.Rates[c.Currency] = money.Rate(c.Rate)
	}
	return q, nil
}
