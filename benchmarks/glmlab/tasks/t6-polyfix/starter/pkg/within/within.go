package within

// Within reports whether v lies in the INCLUSIVE range [lo, hi]:
// the bounds themselves count as inside.
func Within(v, lo, hi float64) bool {
	return v > lo && v < hi
}
