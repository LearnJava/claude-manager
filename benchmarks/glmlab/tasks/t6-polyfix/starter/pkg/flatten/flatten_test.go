package flatten

import (
	"reflect"
	"testing"
)

func TestFlattenAllRows(t *testing.T) {
	if got := Flatten([][]int{{1, 2}, {3}}); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("Flatten([[1 2] [3]]) = %v, want [1 2 3]", got)
	}
}
