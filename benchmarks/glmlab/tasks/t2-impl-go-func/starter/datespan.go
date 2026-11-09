package datespan

import (
	"fmt"
	"time"
)

// ParseISORange parses strings of the form "YYYY-MM-DD..YYYY-MM-DD" where
// both dates are calendar dates in UTC (midnight). Example:
//
//	ParseISORange("2026-01-01..2026-01-05") -> Jan 1, Jan 5 (UTC)
//
// It returns an error if the string is not exactly this shape, if either
// date is not a valid calendar date (e.g. "2026-02-30"), or if start is
// after end (equal dates are allowed).
func ParseISORange(s string) (start, end time.Time, err error) {
	return time.Time{}, time.Time{}, fmt.Errorf("not implemented")
}

// WeekdaysBetween returns how many Monday-Friday days lie in the inclusive
// range [start, end], interpreted as calendar dates in start's location.
// start after end returns 0. A single-day range returns 1 if that day is a
// weekday, else 0.
func WeekdaysBetween(start, end time.Time) int {
	return 0
}
