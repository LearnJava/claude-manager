package invoice

import "testing"

var sample = []Item{
	{Name: "widget", Category: "goods", Quantity: 2, Price: 10.0},
	{Name: "delivery", Category: "shipping", Quantity: 1, Price: 5.0},
	{Name: "service", Category: "fee", Quantity: 3, Price: 4.0},
	{Name: "broken", Category: "goods", Quantity: 0, Price: 100.0},
}

func TestSeasonal(t *testing.T) {
	// 2*10 + 5 + 3*4 = 37 -> 37*0.9
	if got := SeasonalDiscountSubtotal(sample, 0.1); got < 33.3-1e-9 || got > 33.3+1e-9 {
		t.Fatalf("got %v, want 33.3", got)
	}
}

func TestLoyal(t *testing.T) {
	// fees excluded: 20 + 5 = 25 -> 25*0.5
	if got := LoyalCustomerDiscountSubtotal(sample, 0.5); got < 12.5-1e-9 || got > 12.5+1e-9 {
		t.Fatalf("got %v, want 12.5", got)
	}
}

func TestPromo(t *testing.T) {
	// shipping excluded: 20 + 12 = 32 -> 32
	if got := PromoCodeDiscountSubtotal(sample, 0); got < 32-1e-9 || got > 32+1e-9 {
		t.Fatalf("got %v, want 32", got)
	}
}
