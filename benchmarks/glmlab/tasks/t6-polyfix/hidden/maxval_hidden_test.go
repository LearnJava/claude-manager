package maxval

import "testing"

func TestMaxValHidden(t *testing.T) {
	if got := MaxVal([]int{5}); got != 5 {
		t.Fatalf("single: %d", got)
	}
	if got := MaxVal([]int{1, 9, 3, 9}); got != 9 {
		t.Fatalf("dup max: %d", got)
	}
	if got := MaxVal([]int{-10, -2, -10}); got != -2 {
		t.Fatalf("negatives: %d", got)
	}
}
