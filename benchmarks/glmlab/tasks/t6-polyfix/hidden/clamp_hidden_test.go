package clamp

import "testing"

func TestClampHidden(t *testing.T) {
	if got := Clamp(15, 0, 10); got != 10 {
		t.Fatalf("above: %v", got)
	}
	if got := Clamp(5, 0, 10); got != 5 {
		t.Fatalf("inside: %v", got)
	}
	if got := Clamp(-2.5, -3, -1); got != -2.5 {
		t.Fatalf("negative range inside: %v", got)
	}
	if got := Clamp(1, 1, 1); got != 1 {
		t.Fatalf("point range: %v", got)
	}
}
