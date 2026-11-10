package uniq

import (
	"reflect"
	"testing"
)

func TestUniqHidden(t *testing.T) {
	if got := Uniq(nil); len(got) != 0 {
		t.Fatalf("nil: %v", got)
	}
	if got := Uniq([]string{"x", "x", "x"}); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("all same: %v", got)
	}
	if got := Uniq([]string{"z", "m", "z", "a", "m"}); !reflect.DeepEqual(got, []string{"z", "m", "a"}) {
		t.Fatalf("order: %v", got)
	}
}
