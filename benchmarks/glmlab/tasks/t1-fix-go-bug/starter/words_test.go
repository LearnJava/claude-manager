package textstats

import "testing"

func TestWordFrequencyPunctuationAndCase(t *testing.T) {
	got := WordFrequency("Hello, hello world!")
	if got["hello"] != 2 {
		t.Fatalf(`got["hello"] = %d, want 2 (case-insensitive, comma and ! are separators)`, got["hello"])
	}
	if got["world"] != 1 {
		t.Fatalf(`got["world"] = %d, want 1`, got["world"])
	}
}
