package titlecase

import "testing"

func TestTitleCaseHidden(t *testing.T) {
	if got := TitleCase("a B c"); got != "A B C" {
		t.Fatalf("got %q", got)
	}
	if got := TitleCase("привет мир"); got != "Привет Мир" {
		t.Fatalf("cyrillic got %q", got)
	}
	if got := TitleCase(""); got != "" {
		t.Fatalf("empty got %q", got)
	}
	if got := TitleCase("  x  "); got != "X" {
		t.Fatalf("spaces got %q", got)
	}
}
