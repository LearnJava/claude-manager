package countwords

import "testing"

func TestCountWordsSpaces(t *testing.T) {
	if got := CountWords("  hi  "); got != 1 {
		t.Fatalf("CountWords(\"  hi  \") = %d, want 1", got)
	}
}
