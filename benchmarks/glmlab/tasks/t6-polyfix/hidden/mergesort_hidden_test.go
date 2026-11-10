package mergesort

import (
	"reflect"
	"testing"
)

func TestMergeSortedHidden(t *testing.T) {
	if got := MergeSorted(nil, []int{1, 2}); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("empty a: %v", got)
	}
	if got := MergeSorted([]int{1, 2, 2}, []int{2, 3}); !reflect.DeepEqual(got, []int{1, 2, 2, 2, 3}) {
		t.Fatalf("dupes: %v", got)
	}
	if got := MergeSorted([]int{}, []int{}); len(got) != 0 {
		t.Fatalf("both empty: %v", got)
	}
}
