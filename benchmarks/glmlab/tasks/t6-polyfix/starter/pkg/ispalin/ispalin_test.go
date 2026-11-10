package ispalin

import "testing"

func TestIsPalindromeCaseIgnored(t *testing.T) {
	if !IsPalindrome("Шалаш") {
		t.Fatal("IsPalindrome(\"Шалаш\") = false, want true (case-insensitive)")
	}
}
