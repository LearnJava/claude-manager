package experience

import (
	"strings"
	"testing"
)

func TestErrorLine(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"exit code then shell error",
			"Exit code 1\n/usr/bin/bash: -c: line 3: unexpected EOF while looking for matching `\"'",
			"/usr/bin/bash: -c: line 3: unexpected EOF while looking for matching `\"'"},
		{"error-prefixed exit code",
			"Error: Exit code 2\nsome noise\nfatal: not a git repository",
			"fatal: not a git repository"},
		{"traceback", "Exit code 1\nTraceback (most recent call last):\n  File \"x.py\", line 1, in <module>\nValueError: x\n",
			"ValueError: x"},
		{"tool_use_error", "<tool_use_error>File has not been read yet. Read it first before writing to it.</tool_use_error>",
			"File has not been read yet. Read it first before writing to it."},
		{"isolated", "Exit code 1\nsome intro\nThis session is isolated in the worktree C:/w/y; cd elsewhere is blocked",
			"This session is isolated in the worktree C:/w/y; cd elsewhere is blocked"},
		{"no marker takes first", "hello\nworld", "hello"},
		{"only exit code", "Exit code 137", "Exit code 137"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := ErrorLine(c.in); got != c.want {
			t.Errorf("%s: ErrorLine = %q, want %q", c.name, got, c.want)
		}
	}
	long := strings.Repeat("я", 300)
	if got := []rune(ErrorLine(long)); len(got) != 200 {
		t.Errorf("long line: %d runes, want 200", len(got))
	}
}
