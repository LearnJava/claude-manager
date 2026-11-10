package ispalin

// IsPalindrome reports whether s reads the same back-to-front,
// comparing runes and IGNORING letter case.
func IsPalindrome(s string) bool {
	rs := []rune(s)
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		if rs[i] != rs[j] {
			return false
		}
	}
	return true
}
