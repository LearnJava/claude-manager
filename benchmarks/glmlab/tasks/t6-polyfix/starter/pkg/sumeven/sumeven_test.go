package sumeven

import "testing"

func TestSumEvenValues(t *testing.T) {
	if got := SumEven([]int{1, 2, 3}); got != 2 {
		t.Fatalf("SumEven([1 2 3]) = %d, want 2", got)
	}
}
