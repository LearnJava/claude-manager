// Package buildinfo identifies the machine and the manager build that wrote a
// log, so a log review can tell a fixed bug from a live one.
package buildinfo

import (
	"os"
	"runtime/debug"

	"claude-manager/internal/fsutil"
)

// Revision returns the first 7 characters of the VCS revision the binary was
// built from, with "-dirty" when the tree was modified; "unknown" when the
// build carries no VCS data (go test, a build outside git).
func Revision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return revisionFrom(info.Settings)
}

func revisionFrom(settings []debug.BuildSetting) string {
	rev, modified := "", false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev == "" {
		return "unknown"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if modified {
		rev += "-dirty"
	}
	return rev
}

// Host returns the Windows host name as a folder-safe name; "unknown-host"
// when it cannot be read. It deliberately ignores config.toml: a config copied
// to another machine must not merge two machines into one log folder.
func Host() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-host"
	}
	return fsutil.SafeName(h)
}
