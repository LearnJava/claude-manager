package revstr

import "testing"

func TestReverseCyrillic(t *testing.T) {
	if got := Reverse("абв"); got != "вба" {
		t.Fatalf("Reverse(\"абв\") = %q, want %q", got, "вба")
	}
}
