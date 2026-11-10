package fmtdate

import "testing"

func TestFormatDatePadded(t *testing.T) {
	if got := FormatDate(2026, 3, 7); got != "07.03.2026" {
		t.Fatalf("FormatDate(2026, 3, 7) = %q, want 07.03.2026", got)
	}
}
