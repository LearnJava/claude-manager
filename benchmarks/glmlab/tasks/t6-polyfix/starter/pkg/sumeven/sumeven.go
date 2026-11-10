package sumeven

// SumEven returns the sum of the even VALUES in xs
// (values divisible by 2, not items at even positions).
func SumEven(xs []int) int {
	sum := 0
	for i := 0; i < len(xs); i += 2 {
		sum += xs[i]
	}
	return sum
}
