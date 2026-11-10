package clamp

import "testing"

func TestClampBelow(t *testing.T) {
	if got := Clamp(-5, 0, 10); got != 0 {
		t.Fatalf("Clamp(-5, 0, 10) = %v, want 0", got)
	}
}
