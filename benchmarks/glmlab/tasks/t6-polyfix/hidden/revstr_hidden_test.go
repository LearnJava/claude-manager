package revstr

import "testing"

func TestReverseHidden(t *testing.T) {
	if got := Reverse("Anna"); got != "annA" {
		t.Fatalf("got %q", got)
	}
	if got := Reverse("🙂🙂"); got != "🙂🙂" {
		t.Fatalf("emoji got %q", got)
	}
	if got := Reverse(""); got != "" {
		t.Fatalf("empty got %q", got)
	}
	if got := Reverse("x"); got != "x" {
		t.Fatalf("single got %q", got)
	}
}
