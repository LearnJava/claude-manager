package flatten

import (
	"reflect"
	"testing"
)

func TestFlattenHidden(t *testing.T) {
	if got := Flatten([][]int{}); len(got) != 0 {
		t.Fatalf("empty input: %v", got)
	}
	if got := Flatten([][]int{{}, {7}, {}}); !reflect.DeepEqual(got, []int{7}) {
		t.Fatalf("empty rows: %v", got)
	}
	if got := Flatten([][]int{{5, 6}, {7, 8, 9}}); !reflect.DeepEqual(got, []int{5, 6, 7, 8, 9}) {
		t.Fatalf("order: %v", got)
	}
}
