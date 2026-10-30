package optimization

import "testing"

func TestRoute_NoProvider_UnchangedFromComplexityTable(t *testing.T) {
	r := NewModelRouter(nil)
	for _, c := range []Complexity{ComplexityTrivial, ComplexityStandard, ComplexityComplex, ComplexityArchitectural} {
		want := routeByComplexity(c)
		got := r.Route("proj", "", "", string(c))
		if got != want {
			t.Errorf("complexity %s: Route()=%+v, routeByComplexity()=%+v", c, got, want)
		}
	}
}

func TestRoute_NoProvider_AnalystRecommendationUnchanged(t *testing.T) {
	r := NewModelRouter(nil)
	got := r.Route("proj", "opus", "high", "standard")
	want := ModelRecommendation{Model: "opus", Effort: "high", Complexity: "standard", Reason: "Recommended by pre-flight analysis"}
	if got != want {
		t.Errorf("Route()=%+v, want %+v", got, want)
	}
}
