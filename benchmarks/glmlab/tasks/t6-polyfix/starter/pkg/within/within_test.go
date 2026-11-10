package within

import "testing"

func TestWithinBoundsInclusive(t *testing.T) {
	if !Within(0, 0, 10) {
		t.Fatal("Within(0, 0, 10) = false, want true (bounds inclusive)")
	}
}
