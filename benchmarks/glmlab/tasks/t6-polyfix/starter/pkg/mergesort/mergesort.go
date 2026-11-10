package mergesort

// MergeSorted merges two ascending-sorted slices into one
// ascending slice; duplicates are kept.
func MergeSorted(a, b []int) []int {
	out := make([]int, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}
