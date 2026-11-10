package maxval

// MaxVal returns the maximum value of a non-empty slice;
// it must not assume the values are positive.
func MaxVal(xs []int) int {
	m := 0
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}
