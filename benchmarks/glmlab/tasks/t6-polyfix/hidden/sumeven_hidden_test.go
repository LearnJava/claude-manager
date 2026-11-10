package sumeven

import "testing"

func TestSumEvenHidden(t *testing.T) {
	if got := SumEven([]int{2, 4, 5}); got != 6 {
		t.Fatalf("got %d, want 6", got)
	}
	if got := SumEven(nil); got != 0 {
		t.Fatalf("nil: %d", got)
	}
	if got := SumEven([]int{-2, -3, -4}); got != -6 {
		t.Fatalf("negatives: %d, want -6", got)
	}
	if got := SumEven([]int{1, 3, 5}); got != 0 {
		t.Fatalf("no evens: %d", got)
	}
}
