package api

import (
	"testing"
	"time"
)

// TestPeriodBounds is spec 5.4: the period starts on a configurable day, not
// the 1st, and a date before that day belongs to the period that began last
// month.
func TestPeriodBounds(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	}
	cases := []struct {
		now      time.Time
		startDay int
		from, to string
		days     int
		elapsed  int
	}{
		// calendar month
		{day(2026, 9, 13), 1, "2026-09-01", "2026-09-30", 30, 13},
		{day(2026, 9, 1), 1, "2026-09-01", "2026-09-30", 30, 1},
		{day(2026, 9, 30), 1, "2026-09-01", "2026-09-30", 30, 30},
		// salary on the 10th: the 13th is early in the period, the 5th is late
		// in the period that began last month
		{day(2026, 9, 13), 10, "2026-09-10", "2026-10-09", 30, 4},
		{day(2026, 9, 5), 10, "2026-08-10", "2026-09-09", 31, 27},
		{day(2026, 9, 10), 10, "2026-09-10", "2026-10-09", 30, 1},
		{day(2026, 9, 9), 10, "2026-08-10", "2026-09-09", 31, 31},
		// February, and the 28 ceiling on start days
		{day(2026, 2, 15), 1, "2026-02-01", "2026-02-28", 28, 15},
		{day(2026, 3, 1), 28, "2026-02-28", "2026-03-27", 28, 2},
		// year boundary
		{day(2027, 1, 3), 15, "2026-12-15", "2027-01-14", 31, 20},
		// an invalid start day falls back to the 1st
		{day(2026, 9, 13), 0, "2026-09-01", "2026-09-30", 30, 13},
		{day(2026, 9, 13), 31, "2026-09-01", "2026-09-30", 30, 13},
	}
	for _, c := range cases {
		p := periodBounds(c.now, c.startDay)
		if p.From != c.from || p.To != c.to || p.Days != c.days || p.Elapsed != c.elapsed {
			t.Errorf("periodBounds(%s, %d) = %+v, want from %s to %s days %d elapsed %d",
				c.now.Format("2006-01-02"), c.startDay, p, c.from, c.to, c.days, c.elapsed)
		}
	}
}
