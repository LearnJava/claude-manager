"""t4 verify: hidden behavior tests + vet + markers gone."""
import subprocess
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
SANDBOX = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path.cwd()

HIDDEN = '''package invoice

import "testing"

func TestHiddenEdges(t *testing.T) {
	empty := []Item{}
	for _, fn := range []struct {
		name string
		f    func([]Item, float64) float64
	}{
		{"seasonal", SeasonalDiscountSubtotal},
		{"loyal", LoyalCustomerDiscountSubtotal},
		{"promo", PromoCodeDiscountSubtotal},
	} {
		if got := fn.f(empty, 0.3); got != 0 {
			t.Fatalf("%s(empty) = %v, want 0", fn.name, got)
		}
		if got := fn.f(nil, 0.9); got != 0 {
			t.Fatalf("%s(nil) = %v, want 0", fn.name, got)
		}
	}

	neg := []Item{{Name: "refund", Category: "goods", Quantity: 1, Price: -50}}
	if got := SeasonalDiscountSubtotal(neg, 0.2); got != 0 {
		t.Fatalf("negative sum clamped: got %v, want 0", got)
	}

	// both exclusions must be exact, not approximate
	mixed := []Item{
		{Name: "a", Category: "fee", Quantity: 1, Price: 100},
		{Name: "b", Category: "shipping", Quantity: 1, Price: 100},
		{Name: "c", Category: "goods", Quantity: 1, Price: 100},
	}
	if got := LoyalCustomerDiscountSubtotal(mixed, 0.25); got != 150 {
		t.Fatalf("loyal(mixed) = %v, want 150", got)
	}
	if got := PromoCodeDiscountSubtotal(mixed, 0.25); got != 150 {
		t.Fatalf("promo(mixed) = %v, want 150", got)
	}
	if got := SeasonalDiscountSubtotal(mixed, 0.25); got < 225-1e-9 || got > 225+1e-9 {
		t.Fatalf("seasonal(mixed) = %v, want 225", got)
	}
	if got := SeasonalDiscountSubtotal(mixed, 1); got != 0 {
		t.Fatalf("rate=1: got %v, want 0", got)
	}
}
'''

def run(cmd):
    return subprocess.run(cmd, cwd=SANDBOX, capture_output=True, timeout=300)

def main() -> int:
    (SANDBOX / "invoice_hidden_test.go").write_text(HIDDEN, encoding="utf-8")

    for cmd in (["go", "build", "./..."], ["go", "vet", "./..."], ["go", "test", "./..."]):
        r = run(cmd)
        if r.returncode != 0:
            print(" ".join(cmd), "->", r.returncode)
            print((r.stdout + r.stderr).decode("utf-8", "replace")[-1500:])
            print("FAIL")
            return 1

    markers = 0
    for p in SANDBOX.glob("*.go"):
        markers += p.read_text(encoding="utf-8", errors="replace").count("REFACTOR-MARKER")
    if markers:
        print(f"FAIL: {markers} REFACTOR-MARKER comments remain")
        return 1
    print("PASS")
    return 0

if __name__ == "__main__":
    sys.exit(main())
