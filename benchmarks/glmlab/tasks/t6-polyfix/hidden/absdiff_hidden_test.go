package absdiff

import "testing"

func TestAbsDiffHidden(t *testing.T) {
	if got := AbsDiff(5, 2); got != 3 {
		t.Fatalf("got %d", got)
	}
	if got := AbsDiff(4, 4); got != 0 {
		t.Fatalf("equal: %d", got)
	}
	if got := AbsDiff(-3, 1); got != 4 {
		t.Fatalf("negatives: %d", got)
	}
}
