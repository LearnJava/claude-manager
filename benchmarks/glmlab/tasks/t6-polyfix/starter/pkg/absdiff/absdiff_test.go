package absdiff

import "testing"

func TestAbsDiffNegative(t *testing.T) {
	if got := AbsDiff(2, 5); got != 3 {
		t.Fatalf("AbsDiff(2, 5) = %d, want 3", got)
	}
}
