package ispalin

import "testing"

func TestIsPalindromeHidden(t *testing.T) {
	if !IsPalindrome("Anna") {
		t.Fatal("Anna want true")
	}
	if IsPalindrome("ab") {
		t.Fatal("ab want false")
	}
	if !IsPalindrome("") {
		t.Fatal("empty want true")
	}
	if !IsPalindrome("x") {
		t.Fatal("single want true")
	}
	if IsPalindrome("Ab") {
		t.Fatal("Ab want false")
	}
}
