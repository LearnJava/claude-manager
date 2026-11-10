package countwords

import "testing"

func TestCountWordsHidden(t *testing.T) {
	if got := CountWords(""); got != 0 {
		t.Fatalf("empty: %d", got)
	}
	if got := CountWords("one"); got != 1 {
		t.Fatalf("one: %d", got)
	}
	if got := CountWords("a\tb\n c"); got != 3 {
		t.Fatalf("mixed ws: %d", got)
	}
	if got := CountWords("   "); got != 0 {
		t.Fatalf("all-space: %d", got)
	}
}
