package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestRevisionFrom(t *testing.T) {
	cases := []struct {
		name string
		in   []debug.BuildSetting
		want string
	}{
		{"none", nil, "unknown"},
		{"clean", []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}, {Key: "vcs.modified", Value: "false"}}, "0123456"},
		{"dirty", []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}, {Key: "vcs.modified", Value: "true"}}, "0123456-dirty"},
		{"short", []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}, "abc"},
	}
	for _, c := range cases {
		if got := revisionFrom(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRevisionAndHostNonEmpty(t *testing.T) {
	if Revision() == "" {
		t.Error("Revision is empty")
	}
	if Host() == "" {
		t.Error("Host is empty")
	}
}
