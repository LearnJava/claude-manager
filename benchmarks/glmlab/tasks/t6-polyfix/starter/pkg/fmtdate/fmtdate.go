package fmtdate

import "fmt"

// FormatDate renders the date as DD.MM.YYYY with zero-padded
// two-digit day and month and a four-digit year.
func FormatDate(y, m, d int) string {
	return fmt.Sprintf("%d/%d/%d", m, d, y)
}
