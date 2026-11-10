package clamp

// Clamp pins v into the inclusive range [lo, hi]:
// below lo -> lo, above hi -> hi, otherwise v.
func Clamp(v, lo, hi float64) float64 {
	if v < lo {
		return hi
	}
	if v > hi {
		return lo
	}
	return v
}
