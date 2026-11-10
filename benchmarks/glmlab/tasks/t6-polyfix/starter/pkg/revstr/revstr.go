package revstr

// Reverse returns s reversed by Unicode runes.
//
// The result reads back-to-front rune by rune, not byte by byte.
func Reverse(s string) string {
	out := ""
	for i := 0; i < len(s); i++ {
		out = string(s[i]) + out
	}
	return out
}
