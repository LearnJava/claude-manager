package optimization

import "claude-manager/internal/config"

// Complexity levels from pre-flight analysis (PLAN.md 20.3.4).
type Complexity string

const (
	ComplexityTrivial       Complexity = "trivial"
	ComplexityStandard      Complexity = "standard"
	ComplexityComplex       Complexity = "complex"
	ComplexityArchitectural Complexity = "architectural"
)

// ModelRecommendation is the output of the ModelRouter.
type ModelRecommendation struct {
	Model      string `json:"model"`
	Effort     string `json:"effort"`
	Complexity string `json:"complexity"`
	Reason     string `json:"reason"`
}

// ModelRouter maps pre-flight analysis results to model/effort recommendations.
// It is safe for concurrent use (stateless after construction).
type ModelRouter struct {
	cfg *config.OptimizationSettings
}

// NewModelRouter returns a router bound to the given settings.
func NewModelRouter(cfg *config.OptimizationSettings) *ModelRouter {
	return &ModelRouter{cfg: cfg}
}

// Enabled reports whether auto_model_routing is turned on in config.
func (r *ModelRouter) Enabled() bool {
	return r.cfg != nil && r.cfg.AutoModelRouting
}

// Route produces a recommendation from the analysis output fields.
// If the analyst already specified a model it takes precedence over the
// complexity-based routing table; complexity is used as a fallback.
func (r *ModelRouter) Route(recommendedModel, recommendedEffort, complexity string) ModelRecommendation {
	if recommendedModel != "" {
		effort := recommendedEffort
		if effort == "" {
			effort = effortForComplexity(Complexity(complexity))
		}
		return ModelRecommendation{
			Model:      recommendedModel,
			Effort:     effort,
			Complexity: complexity,
			Reason:     "Recommended by pre-flight analysis",
		}
	}
	return routeByComplexity(Complexity(complexity))
}

func routeByComplexity(c Complexity) ModelRecommendation {
	switch c {
	case ComplexityTrivial:
		return ModelRecommendation{
			Model:      "haiku",
			Effort:     "low",
			Complexity: string(c),
			Reason:     "Trivial task — formatting, renaming, version bumps",
		}
	case ComplexityComplex:
		return ModelRecommendation{
			Model:      "sonnet",
			Effort:     "high",
			Complexity: string(c),
			Reason:     "Complex multi-file changes or optimisation",
		}
	case ComplexityArchitectural:
		return ModelRecommendation{
			Model:      "opus",
			Effort:     "high",
			Complexity: string(c),
			Reason:     "Architectural — new subsystems, DB migrations, algorithms",
		}
	default:
		return ModelRecommendation{
			Model:      "sonnet",
			Effort:     "medium",
			Complexity: string(ComplexityStandard),
			Reason:     "Standard task — bug fixes, new endpoints, tests",
		}
	}
}

func effortForComplexity(c Complexity) string {
	switch c {
	case ComplexityTrivial:
		return "low"
	case ComplexityComplex, ComplexityArchitectural:
		return "high"
	default:
		return "medium"
	}
}
