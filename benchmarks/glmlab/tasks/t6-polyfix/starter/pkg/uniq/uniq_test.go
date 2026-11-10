package uniq

import (
	"reflect"
	"testing"
)

func TestUniqKeepsFirstOrder(t *testing.T) {
	if got := Uniq([]string{"b", "a", "b", "c", "a"}); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatalf("got %v, want [b a c]", got)
	}
}
