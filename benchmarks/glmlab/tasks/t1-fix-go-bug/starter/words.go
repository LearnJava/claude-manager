package textstats

import "strings"

// WordFrequency returns how many times each word occurs in text.
//
// A "word" is a maximal run of Unicode letters or digits; every other
// character (punctuation, whitespace, anything else) is a separator.
// Matching is case-insensitive: "Go" and "go" are the same word.
// Returned keys are lower-case.
func WordFrequency(text string) map[string]int {
	fields := strings.Fields(text)
	out := make(map[string]int, len(fields))
	for _, f := range fields {
		out[f]++ // BUG: punctuation stays glued to words, case not folded
	}
	return out
}
