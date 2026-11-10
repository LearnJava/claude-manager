package countwords

import "strings"

// CountWords returns how many whitespace-separated words s has
// (any run of spaces/tabs/newlines is one separator); empty
// or all-space input is 0 words.
func CountWords(s string) int {
	return len(strings.Split(s, " "))
}
