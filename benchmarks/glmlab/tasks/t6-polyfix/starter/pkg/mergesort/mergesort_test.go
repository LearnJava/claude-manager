package mergesort

import (
	"reflect"
	"testing"
)

func TestMergeSortedInterleaved(t *testing.T) {
	if got := MergeSorted([]int{1, 3}, []int{2, 4}); !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Fatalf("MergeSorted([1 3], [2 4]) = %v, want [1 2 3 4]", got)
	}
}
