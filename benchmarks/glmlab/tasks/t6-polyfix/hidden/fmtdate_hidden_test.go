package fmtdate

import "testing"

func TestFormatDateHidden(t *testing.T) {
	if got := FormatDate(2026, 12, 31); got != "31.12.2026" {
		t.Fatalf("got %q", got)
	}
	if got := FormatDate(1999, 1, 1); got != "01.01.1999" {
		t.Fatalf("got %q", got)
	}
}
