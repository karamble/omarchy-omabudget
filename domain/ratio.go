package domain

import "math"

// Every ratio the ledger reports is computed here, from reference amounts,
// so it reads the same whatever currency the figures are shown in.

// pct is part as a whole percentage of whole, truncated toward zero, and 0
// when there is no whole to measure against. Alerts fire on it, so it never
// rounds up.
func pct(part, whole int64) int {
	if whole <= 0 {
		return 0
	}
	return int(part * 100 / whole)
}

// deltaPct is delta as a percentage of before's size, to one decimal, so it
// carries delta's sign even from an overdraft. Nil when there is nothing to
// compare with.
func deltaPct(delta, before int64) *float64 {
	if before == 0 {
		return nil
	}
	v := math.Round(float64(delta)*1000/math.Abs(float64(before))) / 10
	if v == 0 {
		v = 0 // a drop that rounds away is not minus zero
	}
	return &v
}
