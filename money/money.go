// Package money represents amounts as integer minor units per commodity.
// Nothing here is a float: arithmetic is exact, so converting the same amount
// at the same rate gives the same number every time.
package money

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Amount is a quantity of one commodity in its minor unit, so EUR 4.50 is
// {450, "EUR"} and DCR 0.00000001 is {1, "DCR"}.
type Amount struct {
	Minor    int64  `json:"minor"`
	Currency string `json:"currency"`
}

// decimals is how many minor-unit digits a commodity carries. Two unless the
// commodity says otherwise. Fiat without minor units, and cryptocurrencies
// with eight, are the cases that matter here.
var decimals = map[string]int{
	"JPY": 0, "KRW": 0, "HUF": 0, "ISK": 0, "CLP": 0, "VND": 0,
	"BHD": 3, "KWD": 3, "OMR": 3, "JOD": 3, "TND": 3,
	"BTC": 8, "DCR": 8, "LTC": 8,
	"ETH": 18,
}

// Decimals reports the minor-unit digits of a commodity.
func Decimals(currency string) int {
	if d, ok := decimals[strings.ToUpper(currency)]; ok {
		return d
	}
	return 2
}

// scale is 10^Decimals(currency).
func scale(currency string) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(Decimals(currency))), nil)
}

var (
	ErrZero      = errors.New("amount is zero")
	ErrEmpty     = errors.New("no amount given")
	ErrTooFine   = errors.New("more decimal places than the currency has")
	ErrMalformed = errors.New("not an amount")
	ErrCurrency  = errors.New("currencies differ")
	ErrOverflow  = errors.New("amount too large")
)

// Parse reads an amount typed by a person: "4.50", "-12", "1,250.00",
// "23.50+18" (spec 4.1: inline arithmetic evaluates on entry). Whitespace and
// thousands separators are ignored; the decimal separator is a dot or, when
// there is no dot, a comma. Only + and - are evaluated; anything else is a
// malformed amount rather than a guess.
//
// Zero is an error by spec rule (Amount = 0: block save), and so are more
// decimal places than the currency has, since rounding money silently is how
// ledgers stop balancing.
func Parse(text, currency string) (Amount, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return Amount{}, ErrEmpty
	}
	total := new(big.Rat)
	sign := 1
	terms := 0
	term := strings.Builder{}
	flush := func() error {
		if term.Len() == 0 {
			return nil
		}
		r, err := parseTerm(term.String())
		if err != nil {
			return err
		}
		if sign < 0 {
			r.Neg(r)
		}
		total.Add(total, r)
		terms++
		term.Reset()
		return nil
	}
	for i, c := range s {
		switch {
		case c == '+' || c == '-':
			// A leading sign, or one following an operator, belongs to the term.
			if term.Len() == 0 && (i == 0 || strings.ContainsRune("+-", rune(s[i-1]))) {
				if c == '-' {
					sign = -sign
				}
				continue
			}
			if err := flush(); err != nil {
				return Amount{}, err
			}
			sign = 1
			if c == '-' {
				sign = -1
			}
		case c == ' ' || c == '_':
		case (c >= '0' && c <= '9') || c == '.' || c == ',':
			term.WriteRune(c)
		default:
			return Amount{}, fmt.Errorf("%w: %q", ErrMalformed, text)
		}
	}
	if err := flush(); err != nil {
		return Amount{}, err
	}
	// A bare sign is nothing typed, not a zero.
	if terms == 0 {
		return Amount{}, ErrEmpty
	}
	return fromRat(total, currency)
}

// parseTerm reads one decimal literal into a rational.
func parseTerm(t string) (*big.Rat, error) {
	// "1,250.00" has a thousands comma; "4,50" has a decimal comma.
	if strings.Contains(t, ".") {
		t = strings.ReplaceAll(t, ",", "")
	} else if strings.Count(t, ",") == 1 {
		t = strings.Replace(t, ",", ".", 1)
	} else {
		t = strings.ReplaceAll(t, ",", "")
	}
	if t == "" || t == "." {
		return nil, fmt.Errorf("%w: %q", ErrMalformed, t)
	}
	r, ok := new(big.Rat).SetString(t)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrMalformed, t)
	}
	return r, nil
}

// fromRat converts an exact rational to minor units, refusing to round.
func fromRat(r *big.Rat, currency string) (Amount, error) {
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale(currency)))
	if !scaled.IsInt() {
		return Amount{}, ErrTooFine
	}
	n := scaled.Num()
	if !n.IsInt64() {
		return Amount{}, ErrOverflow
	}
	if n.Sign() == 0 {
		return Amount{}, ErrZero
	}
	return Amount{Minor: n.Int64(), Currency: strings.ToUpper(currency)}, nil
}

// New builds an amount from minor units without parsing.
func New(minor int64, currency string) Amount {
	return Amount{Minor: minor, Currency: strings.ToUpper(currency)}
}

// IsZero reports an empty amount.
func (a Amount) IsZero() bool { return a.Minor == 0 }

// Neg flips the sign.
func (a Amount) Neg() Amount { return Amount{Minor: -a.Minor, Currency: a.Currency} }

// Abs drops the sign.
func (a Amount) Abs() Amount {
	if a.Minor < 0 {
		return a.Neg()
	}
	return a
}

// Add sums two amounts of the same commodity.
func (a Amount) Add(b Amount) (Amount, error) {
	if a.Currency != b.Currency {
		return Amount{}, fmt.Errorf("%w: %s and %s", ErrCurrency, a.Currency, b.Currency)
	}
	sum := a.Minor + b.Minor
	// Same-sign operands that produce the opposite sign overflowed.
	if (a.Minor > 0 && b.Minor > 0 && sum < 0) || (a.Minor < 0 && b.Minor < 0 && sum > 0) {
		return Amount{}, ErrOverflow
	}
	return Amount{Minor: sum, Currency: a.Currency}, nil
}

// Sum adds a list, which is how split lines are checked against their parent.
func Sum(currency string, amounts ...Amount) (Amount, error) {
	total := Amount{Currency: strings.ToUpper(currency)}
	for _, a := range amounts {
		var err error
		if total, err = total.Add(a); err != nil {
			return Amount{}, err
		}
	}
	return total, nil
}

// Format renders an amount the way a person writes it: "4.50", "-1250.00",
// "0.00000001". No thousands separators; the view adds those.
func (a Amount) Format() string {
	d := Decimals(a.Currency)
	neg := a.Minor < 0
	n := new(big.Int).SetInt64(a.Minor)
	n.Abs(n)
	s := n.String()
	if d > 0 {
		for len(s) <= d {
			s = "0" + s
		}
		s = s[:len(s)-d] + "." + s[len(s)-d:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// String is Format with the currency, for logs and errors.
func (a Amount) String() string { return a.Format() + " " + a.Currency }

// Rate is an exchange rate kept as an exact string, so it can be stored on a
// transaction and re-applied later to the same result: a decimal, "4.3125",
// or a fraction, "47/184", which is what crossing two rates produces.
type Rate string

// parseRate reads a rate, refusing anything that is not a positive number.
func parseRate(rate Rate) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(string(rate))
	if !ok || r.Sign() <= 0 {
		return nil, fmt.Errorf("%w: rate %q", ErrMalformed, rate)
	}
	return r, nil
}

// Equal reports whether two rates are the same number, so "0.235" and
// "0.2350" agree. A rate that does not parse equals nothing.
func (r Rate) Equal(o Rate) bool {
	a, err := parseRate(r)
	if err != nil {
		return false
	}
	b, err := parseRate(o)
	if err != nil {
		return false
	}
	return a.Cmp(b) == 0
}

// CrossRate derives the rate from one commodity to another when both are
// quoted against the same reference: with 1 FROM = from REF and 1 TO = to REF,
// 1 FROM = from/to TO. The quotient is kept as a fraction, so converting
// through it rounds once, at the target's minor unit. Equal rates cross at 1
// without being read, so a commodity converts to itself even when no rate is
// on file for it.
func CrossRate(from, to Rate) (Rate, error) {
	if from == to {
		return "1", nil
	}
	f, err := parseRate(from)
	if err != nil {
		return "", err
	}
	t, err := parseRate(to)
	if err != nil {
		return "", err
	}
	return Rate(new(big.Rat).Quo(f, t).RatString()), nil
}

// Convert applies a rate to produce the amount in the target commodity,
// rounding half away from zero to that commodity's minor unit. This is the
// one place rounding happens, and its result is what gets stored.
func Convert(a Amount, rate Rate, to string) (Amount, error) {
	r, err := parseRate(rate)
	if err != nil {
		return Amount{}, err
	}
	// original minor / original scale * rate * target scale
	v := new(big.Rat).SetInt64(a.Minor)
	v.Quo(v, new(big.Rat).SetInt(scale(a.Currency)))
	v.Mul(v, r)
	v.Mul(v, new(big.Rat).SetInt(scale(to)))
	minor, err := roundHalfAway(v)
	if err != nil {
		return Amount{}, err
	}
	return Amount{Minor: minor, Currency: strings.ToUpper(to)}, nil
}

// roundHalfAway rounds a rational to the nearest integer, halves away from
// zero, which is what bank statements do. A result outside int64 is an
// overflow rather than a wrapped number.
func roundHalfAway(r *big.Rat) (int64, error) {
	num := new(big.Int).Set(r.Num())
	den := r.Denom()
	neg := num.Sign() < 0
	num.Abs(num)
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	// 2*rem >= den means the fraction is at least a half.
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(den) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if neg {
		q.Neg(q)
	}
	if !q.IsInt64() {
		return 0, ErrOverflow
	}
	return q.Int64(), nil
}
