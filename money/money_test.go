package money

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in       string
		currency string
		want     int64
		err      error
	}{
		{"4.50", "EUR", 450, nil},
		{"4,50", "EUR", 450, nil},
		{"1,250.00", "EUR", 125000, nil},
		{"-12", "EUR", -1200, nil},
		{"+12", "EUR", 1200, nil},
		{"  7 ", "EUR", 700, nil},
		{".5", "EUR", 50, nil},
		{"3.", "EUR", 300, nil},
		// spec 4.1: inline arithmetic evaluates on entry
		{"23.50+18", "EUR", 4150, nil},
		{"23.50 + 18", "EUR", 4150, nil},
		{"100-0.01", "EUR", 9999, nil},
		{"10+20+30", "EUR", 6000, nil},
		{"-5-5", "EUR", -1000, nil},
		{"50--10", "EUR", 6000, nil},
		// commodities with other minor units
		{"1500", "JPY", 1500, nil},
		{"0.00000001", "DCR", 1, nil},
		{"1.5", "DCR", 150000000, nil},
		// refusals
		{"", "EUR", 0, ErrEmpty},
		{"0", "EUR", 0, ErrZero},
		{"0.00", "EUR", 0, ErrZero},
		{"10-10", "EUR", 0, ErrZero},
		{"4.505", "EUR", 0, ErrTooFine},
		{"1.5", "JPY", 0, ErrTooFine},
		{"12*2", "EUR", 0, ErrMalformed},
		{"abc", "EUR", 0, ErrMalformed},
		{"1.2.3", "EUR", 0, ErrMalformed},
		{"+", "EUR", 0, ErrEmpty},
	}
	for _, c := range cases {
		got, err := Parse(c.in, c.currency)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("Parse(%q, %s) err = %v, want %v", c.in, c.currency, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q, %s) unexpected error: %v", c.in, c.currency, err)
			continue
		}
		if got.Minor != c.want {
			t.Errorf("Parse(%q, %s) = %d, want %d", c.in, c.currency, got.Minor, c.want)
		}
		if got.Currency != c.currency {
			t.Errorf("Parse(%q, %s) currency = %q", c.in, c.currency, got.Currency)
		}
	}
}

// TestRoundTrip: what Format prints, Parse reads back to the same minor units.
func TestRoundTrip(t *testing.T) {
	for _, a := range []Amount{
		New(450, "EUR"), New(-125000, "EUR"), New(1, "EUR"), New(-1, "EUR"),
		New(1500, "JPY"), New(1, "DCR"), New(150000000, "DCR"),
		New(9223372036854775807, "EUR"),
	} {
		s := a.Format()
		back, err := Parse(s, a.Currency)
		if err != nil {
			t.Errorf("Parse(Format(%v)=%q) failed: %v", a, s, err)
			continue
		}
		if back != a {
			t.Errorf("round trip %v -> %q -> %v", a, s, back)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		a    Amount
		want string
	}{
		{New(450, "EUR"), "4.50"},
		{New(5, "EUR"), "0.05"},
		{New(-5, "EUR"), "-0.05"},
		{New(125000, "EUR"), "1250.00"},
		{New(1500, "JPY"), "1500"},
		{New(1, "DCR"), "0.00000001"},
		{New(-100000000, "DCR"), "-1.00000000"},
	}
	for _, c := range cases {
		if got := c.a.Format(); got != c.want {
			t.Errorf("Format(%v) = %q, want %q", c.a, got, c.want)
		}
	}
}

// TestSplitRemainder is spec 1.3 and 4.2: lines must sum to the parent, and
// the remainder is what the form shows while they do not.
func TestSplitRemainder(t *testing.T) {
	parent := New(25500, "EUR") // a supermarket receipt of 255.00
	lines := []Amount{New(18000, "EUR"), New(4000, "EUR"), New(3500, "EUR")}

	sum, err := Sum("EUR", lines...)
	if err != nil {
		t.Fatal(err)
	}
	remainder, err := parent.Add(sum.Neg())
	if err != nil {
		t.Fatal(err)
	}
	if !remainder.IsZero() {
		t.Errorf("lines 180+40+35 should equal 255, remainder %v", remainder)
	}

	short, _ := Sum("EUR", lines[:2]...)
	rem, _ := parent.Add(short.Neg())
	if rem.Minor != 3500 {
		t.Errorf("with one line missing the remainder should be 35.00, got %v", rem)
	}
}

// TestTransferLegs: a transfer is one object shown in both ledgers with
// opposite signs, and the two legs must cancel exactly.
func TestTransferLegs(t *testing.T) {
	out := New(-50000, "EUR")
	in := out.Neg()
	total, err := out.Add(in)
	if err != nil {
		t.Fatal(err)
	}
	if !total.IsZero() {
		t.Errorf("transfer legs sum to %v, want zero", total)
	}
}

func TestAddRefusesMixedCurrencies(t *testing.T) {
	if _, err := New(100, "EUR").Add(New(100, "PLN")); !errors.Is(err, ErrCurrency) {
		t.Errorf("err = %v, want ErrCurrency", err)
	}
}

func TestAddOverflow(t *testing.T) {
	if _, err := New(9223372036854775807, "EUR").Add(New(1, "EUR")); !errors.Is(err, ErrOverflow) {
		t.Errorf("err = %v, want ErrOverflow", err)
	}
}

// TestConvert covers spec 8: the base amount is computed once from the rate at
// entry and rounded half away from zero to the base currency's minor unit.
func TestConvert(t *testing.T) {
	cases := []struct {
		a    Amount
		rate Rate
		base string
		want int64
	}{
		{New(10000, "PLN"), "0.2320", "EUR", 2320},   // 100 PLN at 0.232
		{New(1, "PLN"), "0.2320", "EUR", 0},          // 0.01 PLN rounds to nothing
		{New(3, "PLN"), "0.2320", "EUR", 1},          // 0.03 PLN = 0.00696, rounds up
		{New(-10000, "PLN"), "0.2320", "EUR", -2320}, // sign preserved
		{New(100000000, "DCR"), "18.5", "EUR", 1850}, // 1 DCR at 18.50
		{New(1000, "JPY"), "0.00615", "EUR", 615},    // 1000 JPY
		{New(12345, "EUR"), "1", "EUR", 12345},       // identity
		{New(150, "EUR"), "0.5", "EUR", 75},          // exact half stays exact
		{New(1, "EUR"), "0.5", "EUR", 1},             // 0.005 rounds half away, to 0.01
	}
	for _, c := range cases {
		got, err := Convert(c.a, c.rate, c.base)
		if err != nil {
			t.Errorf("Convert(%v, %s, %s) error: %v", c.a, c.rate, c.base, err)
			continue
		}
		if got.Minor != c.want || got.Currency != c.base {
			t.Errorf("Convert(%v, %s, %s) = %v, want %d %s", c.a, c.rate, c.base, got, c.want, c.base)
		}
	}
}

// TestConvertIsDeterministic: a stored base amount can be re-derived from the
// row's own amount and rate and land on the same number, and only a different
// rate moves it.
func TestConvertIsDeterministic(t *testing.T) {
	a := New(10000, "PLN")
	first, _ := Convert(a, "0.2320", "EUR")
	again, _ := Convert(a, "0.2320", "EUR")
	later, _ := Convert(a, "0.2500", "EUR")
	if first != again {
		t.Errorf("the same rate gave %v then %v", first, again)
	}
	if first == later {
		t.Fatal("rates differ, results should differ")
	}
	if first.Minor != 2320 {
		t.Errorf("100 PLN at 0.2320 should read 23.20, got %v", first)
	}
}

func TestConvertRefusesBadRate(t *testing.T) {
	for _, r := range []Rate{"", "0", "-1", "abc"} {
		if _, err := Convert(New(100, "PLN"), r, "EUR"); err == nil {
			t.Errorf("rate %q should be refused", r)
		}
	}
}

// TestCrossRate: two rates against one reference cross into a rate between
// the two commodities, kept as a fraction in lowest terms.
func TestCrossRate(t *testing.T) {
	cases := []struct {
		from, to Rate
		want     Rate
	}{
		{"0.5", "0.25", "2"},
		{"0.25", "0.5", "1/2"},
		{"1", "8", "1/8"},
		{"0.235", "0.92", "47/184"},
		{"1", "3", "1/3"},
		{"2", "4", "1/2"},
	}
	for _, c := range cases {
		got, err := CrossRate(c.from, c.to)
		if err != nil {
			t.Errorf("CrossRate(%s, %s) error: %v", c.from, c.to, err)
			continue
		}
		if got != c.want {
			t.Errorf("CrossRate(%s, %s) = %s, want %s", c.from, c.to, got, c.want)
		}
	}
}

// TestCrossRateIdentity: a commodity crossed with itself is 1 before any rate
// is read, so nothing on file is needed for it, and nothing on file can
// change it.
func TestCrossRateIdentity(t *testing.T) {
	for _, r := range []Rate{"", "0", "abc", "0.235"} {
		got, err := CrossRate(r, r)
		if err != nil || got != "1" {
			t.Errorf("CrossRate(%q, %q) = %q, %v; want 1", r, r, got, err)
		}
	}
}

// TestCrossRateThroughReference: converting through a cross rounds once, at
// the target. A rate trimmed to six places on the way would land elsewhere.
func TestCrossRateThroughReference(t *testing.T) {
	cross, err := CrossRate("0.235", "0.92") // PLN and USD, both against EUR
	if err != nil {
		t.Fatal(err)
	}
	// 1,000,000 PLN = 1,000,000 * 47/184 USD = 255434.7826... USD
	got, err := Convert(New(100000000, "PLN"), cross, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if got.Minor != 25543478 {
		t.Errorf("exact cross gives %v, want 255434.78 USD", got)
	}
	trimmed, _ := Convert(New(100000000, "PLN"), "0.255435", "USD")
	if trimmed.Minor == got.Minor {
		t.Error("a trimmed rate should land on a different cent, or this test proves nothing")
	}
	// Sign is preserved through a fractional rate.
	neg, err := Convert(New(-100000000, "PLN"), cross, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if neg.Minor != -25543478 {
		t.Errorf("negative cross gives %v, want -255434.78 USD", neg)
	}
}

func TestCrossRateRefusesZeroAndNegative(t *testing.T) {
	cases := []struct{ from, to Rate }{
		{"0.5", "0"}, {"0", "0.5"}, {"0.5", "-1"}, {"-1", "0.5"}, {"0.5", "abc"}, {"", "0.5"},
	}
	for _, c := range cases {
		if _, err := CrossRate(c.from, c.to); !errors.Is(err, ErrMalformed) {
			t.Errorf("CrossRate(%q, %q) err = %v, want ErrMalformed", c.from, c.to, err)
		}
	}
}

// TestConvertOverflow: a commodity with eighteen minor digits runs out of
// int64 at a little over nine of its units, and the answer is a refusal
// rather than a wrapped number.
func TestConvertOverflow(t *testing.T) {
	if _, err := Convert(New(1000, "EUR"), "1", "ETH"); !errors.Is(err, ErrOverflow) {
		t.Errorf("10 EUR into ETH minor units: err = %v, want ErrOverflow", err)
	}
	if _, err := Convert(New(9223372036854775807, "EUR"), "2", "EUR"); !errors.Is(err, ErrOverflow) {
		t.Errorf("doubling MaxInt64: err = %v, want ErrOverflow", err)
	}
	if _, err := Convert(New(-9223372036854775807, "EUR"), "2", "EUR"); !errors.Is(err, ErrOverflow) {
		t.Errorf("doubling -MaxInt64: err = %v, want ErrOverflow", err)
	}
	// Just inside the limit still converts.
	if got, err := Convert(New(900, "EUR"), "1", "ETH"); err != nil || got.Minor != 9000000000000000000 {
		t.Errorf("9 EUR into ETH = %v, %v", got, err)
	}
}

// TestConvertAcceptsFraction: a rate written as num/den is read exactly.
func TestConvertAcceptsFraction(t *testing.T) {
	got, err := Convert(New(300, "EUR"), "1/3", "EUR")
	if err != nil || got.Minor != 100 {
		t.Errorf("3.00 at 1/3 = %v, %v; want 1.00", got, err)
	}
}

func TestRateEqual(t *testing.T) {
	cases := []struct {
		a, b Rate
		want bool
	}{
		{"0.235", "0.2350", true},
		{"1", "1.0", true},
		{"1/4", "0.25", true},
		{"0.235", "0.24", false},
		{"abc", "abc", false},
		{"", "", false},
		{"0", "0", false},
	}
	for _, c := range cases {
		if got := c.a.Equal(c.b); got != c.want {
			t.Errorf("Rate(%q).Equal(%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
