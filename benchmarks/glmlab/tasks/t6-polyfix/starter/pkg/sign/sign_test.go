package sign

import "testing"

func TestSignZero(t *testing.T) {
	if got := Sign(0); got != 0 {
		t.Fatalf("Sign(0) = %d, want 0", got)
	}
}
