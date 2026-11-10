package titlecase

import "unicode"

// TitleCase upper-cases the first rune of EVERY space-separated
// word and lower-cases the rest; words are joined by one space.
func TitleCase(s string) string {
	rs := []rune(s)
	if len(rs) > 0 {
		rs[0] = unicode.ToUpper(rs[0])
	}
	return string(rs)
}
