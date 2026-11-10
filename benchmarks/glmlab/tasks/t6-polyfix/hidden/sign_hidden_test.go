package sign

import "testing"

func TestSignHidden(t *testing.T) {
	if got := Sign(-5); got != -1 {
		t.Fatalf("negative: %d", got)
	}
	if got := Sign(7); got != 1 {
		t.Fatalf("positive: %d", got)
	}
}
