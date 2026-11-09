package invoice

// Item is one line of an invoice.
type Item struct {
	Name     string
	Category string
	Quantity int
	Price    float64 // unit price
}

// REFACTOR-MARKER: copy-paste triple below — extract the shared logic into
// one helper; keep these three exported functions and their behavior.

// SeasonalDiscountSubtotal sums Quantity*Price over all items with
// Quantity > 0 and returns that sum reduced by rate (0 <= rate <= 1).
// A negative sum is never returned.
func SeasonalDiscountSubtotal(items []Item, rate float64) float64 {
	total := 0.0
	for _, it := range items {
		if it.Quantity <= 0 {
			continue
		}
		total += float64(it.Quantity) * it.Price
	}
	if total < 0 {
		return 0
	}
	return total * (1 - rate)
}

// LoyalCustomerDiscountSubtotal is SeasonalDiscountSubtotal but items in
// the category "fee" are excluded from the sum.
func LoyalCustomerDiscountSubtotal(items []Item, rate float64) float64 {
	total := 0.0
	for _, it := range items {
		if it.Quantity <= 0 {
			continue
		}
		if it.Category == "fee" {
			continue
		}
		total += float64(it.Quantity) * it.Price
	}
	if total < 0 {
		return 0
	}
	return total * (1 - rate)
}

// PromoCodeDiscountSubtotal is SeasonalDiscountSubtotal but items in
// the category "shipping" are excluded from the sum.
func PromoCodeDiscountSubtotal(items []Item, rate float64) float64 {
	total := 0.0
	for _, it := range items {
		if it.Quantity <= 0 {
			continue
		}
		if it.Category == "shipping" {
			continue
		}
		total += float64(it.Quantity) * it.Price
	}
	if total < 0 {
		return 0
	}
	return total * (1 - rate)
}
