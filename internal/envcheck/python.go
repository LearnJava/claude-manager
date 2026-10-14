package envcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
)

// WindowsInstalledPython returns the newest python.exe from the standard
// per-user install locations (the Python install manager and the classic
// per-user installer), or "". These installs are often not on PATH, where
// only the Microsoft Store stub is.
func WindowsInstalledPython() string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return ""
	}
	var found []string
	for _, pattern := range []string{
		filepath.Join(local, "Python", "pythoncore-*", "python.exe"),
		filepath.Join(local, "Programs", "Python", "Python3*", "python.exe"),
	} {
		m, _ := filepath.Glob(pattern)
		found = append(found, m...)
	}
	return NewestPython(found)
}

var (
	dottedVersion  = regexp.MustCompile(`(\d+)\.(\d+)(t?)`)    // pythoncore-3.14-64, pythoncore-3.14t-64
	compactVersion = regexp.MustCompile(`(?i)python(\d)(\d+)`) // Python314
)

// pythonRank orders install directories by version: [major, minor, build],
// where build is 1 for a regular build and 0 for a free-threaded ("t") one.
// An unrecognised name ranks below every recognised one.
func pythonRank(path string) []int {
	dir := filepath.Base(filepath.Dir(path))
	if m := dottedVersion.FindStringSubmatch(dir); m != nil {
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		build := 1
		if m[3] == "t" {
			build = 0
		}
		return []int{major, minor, build}
	}
	if m := compactVersion.FindStringSubmatch(dir); m != nil {
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		return []int{major, minor, 1}
	}
	return []int{-1}
}

// NewestPython picks the python.exe with the highest version. Versions are
// compared as numbers: a plain string sort ranks 3.9 above 3.14.
func NewestPython(paths []string) string {
	best := ""
	var bestRank []int
	for _, p := range paths {
		r := pythonRank(p)
		if best == "" || slices.Compare(r, bestRank) > 0 || (slices.Compare(r, bestRank) == 0 && p < best) {
			best, bestRank = p, r
		}
	}
	return best
}
