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
// It is safe for concurrent use (stateless after construction) except for
// SetOutcomeProvider, which is meant to be called once during wiring, before
// the router is shared across goroutines — same convention as
// SessionManager.SetActionIndexer/SetPrimerBuilder.
type ModelRouter struct {
	cfg      *config.OptimizationSettings
	outcomes OutcomeProvider
}

// NewModelRouter returns a router bound to the given settings.
func NewModelRouter(cfg *config.OptimizationSettings) *ModelRouter {
	return &ModelRouter{cfg: cfg}
}

// SetOutcomeProvider wires in measured-outcome-based routing (LEARN-TASKS.md
// LN-13): once set, Route() may recommend a lower-tier model than the
// complexity table's default when the project's own history shows it
// completing reliably and more cheaply. Never called means Route behaves
// exactly as before this feature existed.
func (r *ModelRouter) SetOutcomeProvider(p OutcomeProvider) {
	r.outcomes = p
}

// Enabled reports whether auto_model_routing is turned on in config.
func (r *ModelRouter) Enabled() bool {
	return r.cfg != nil && r.cfg.AutoModelRouting
}

// Route produces a recommendation from the analysis output fields.
// If the analyst already specified a model it takes precedence over the
// complexity-based routing table; complexity is used as a fallback.
//
// When an OutcomeProvider is wired (SetOutcomeProvider) and project is
// non-empty, the table/analyst recommendation is then treated as a
// baseline that measured outcomes for this project may downgrade — see
// applyOutcomeOverride. With no provider wired, or project == "", this is
// byte-identical to the pre-LN-13 behavior.
func (r *ModelRouter) Route(project, recommendedModel, recommendedEffort, complexity string) ModelRecommendation {
	var base ModelRecommendation
	if recommendedModel != "" {
		effort := recommendedEffort
		if effort == "" {
			effort = effortForComplexity(Complexity(complexity))
		}
		base = ModelRecommendation{
			Model:      recommendedModel,
			Effort:     effort,
			Complexity: complexity,
			Reason:     "Recommended by pre-flight analysis",
		}
	} else {
		base = routeByComplexity(Complexity(complexity))
	}
	return r.applyOutcomeOverride(project, base)
}

// applyOutcomeOverride downgrades base to a measurably cheaper, reliable,
// lower-tier model when the project's history supports it. Never applies to
// ComplexityArchitectural (LEARN-TASKS.md LN-13: "никогда не понижать модель
// для architectural") and never on error/empty data — it only ever replaces
// base, it never invents a recommendation the table/analyst didn't already
// make first.
func (r *ModelRouter) applyOutcomeOverride(project string, base ModelRecommendation) ModelRecommendation {
	if r.outcomes == nil || project == "" || Complexity(base.Complexity) == ComplexityArchitectural {
		return base
	}
	stats, err := r.outcomes.OutcomeStats(project, Complexity(base.Complexity))
	if err != nil || len(stats) == 0 {
		return base
	}
	cand, baseCost, ok := evaluateOutcome(stats, base.Model)
	if !ok {
		return base
	}
	return ModelRecommendation{
		Model:      cand.Model,
		Effort:     cand.Effort,
		Complexity: base.Complexity,
		Reason:     outcomeReason(cand, base.Model, baseCost),
	}
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
