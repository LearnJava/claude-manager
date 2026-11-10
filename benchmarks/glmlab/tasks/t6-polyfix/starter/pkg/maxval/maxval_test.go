package maxval

import "testing"

func TestMaxValAllNegative(t *testing.T) {
	if got := MaxVal([]int{-3, -1, -7}); got != -1 {
		t.Fatalf("MaxVal([-3 -1 -7]) = %d, want -1", got)
	}
}
