package sign

// Sign returns -1 for negative v, 0 for exactly zero,
// and 1 for positive v.
func Sign(v int) int {
	if v < 0 {
		return -1
	}
	return 1
}
