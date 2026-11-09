package textstats

import "testing"

func TestWordFrequencyUnicodeAndDigits(t *testing.T) {
	got := WordFrequency("Привет, мир! привет, мир №1 раз, и Раз-2.")
	if got["привет"] != 2 {
		t.Fatalf(`got["привет"] = %d, want 2`, got["привет"])
	}
	if got["мир"] != 2 {
		t.Fatalf(`got["мир"] = %d, want 2`, got["мир"])
	}
	if got["1"] != 1 || got["2"] != 1 {
		t.Fatalf(`digits: got %v, want 1:1`, got)
	}
	if got["раз"] != 2 {
		t.Fatalf(`got["раз"] = %d, want 2 (hyphen is a separator)`, got["раз"])
	}
	if _, ok := got["и"]; !ok {
		t.Fatalf(`single-letter word "и" lost: %v`, got)
	}
}

func TestWordFrequencySeparatorsAndEmpty(t *testing.T) {
	got := WordFrequency("  a\tb\n\na \t -- a!  ")
	if got["a"] != 3 || got["b"] != 1 {
		t.Fatalf(`got %v, want a:3 b:1`, got)
	}
	if n := len(WordFrequency("   \t\n !!! ... ")); n != 0 {
		t.Fatalf("separators-only: got %d words, want 0", n)
	}
	if n := len(WordFrequency("")); n != 0 {
		t.Fatalf("empty: got %d words, want 0", n)
	}
	if got := WordFrequency("СлОвО word WORD слово"); got["слово"] != 2 || got["word"] != 2 {
		t.Fatalf("case folding (incl. cyrillic): got %v", got)
	}
}
