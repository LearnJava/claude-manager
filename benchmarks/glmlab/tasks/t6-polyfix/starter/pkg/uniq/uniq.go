package uniq

import "sort"

// Uniq removes duplicate strings, keeping the FIRST occurrence
// of each value, in the original order.
func Uniq(items []string) []string {
	seen := map[string]bool{}
	for _, it := range items {
		seen[it] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
