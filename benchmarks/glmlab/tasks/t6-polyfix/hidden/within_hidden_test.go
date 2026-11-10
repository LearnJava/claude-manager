package within

import "testing"

func TestWithinHidden(t *testing.T) {
	if !Within(10, 0, 10) {
		t.Fatal("upper bound want true")
	}
	if Within(-0.1, 0, 10) {
		t.Fatal("below want false")
	}
	if Within(10.1, 0, 10) {
		t.Fatal("above want false")
	}
	if !Within(5, 0, 10) {
		t.Fatal("inside want true")
	}
}
