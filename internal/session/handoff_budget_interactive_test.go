package session

import (
	"strings"
	"testing"

	"claude-manager/internal/config"
)

func newInteractiveBudgetSession(budget int) *Session {
	return New(Params{
		ID:     "p/s",
		Config: config.SessionConfig{Name: "s", HandoffContextTokens: budget},
	})
}

func lastCall(ctx int) *TokenUsage {
	return &TokenUsage{InputTokens: 3, CacheCreationInputTokens: 1000, CacheReadInputTokens: ctx - 1003}
}

func TestInteractiveBudget_AsksOverBudget(t *testing.T) {
	s := newInteractiveBudgetSession(150_000)
	s.checkInteractiveBudget(lastCall(160_000), false)
	pq := s.PendingQuestion()
	if pq == nil {
		t.Fatal("want a question")
	}
	if !strings.Contains(pq.Question, "160k") || !strings.Contains(pq.Question, "150k") {
		t.Errorf("question must name tokens: %q", pq.Question)
	}
	if len(pq.Options) != len(contextRestartOptions) || pq.Options[2] != contextRestartOptions[2] {
		t.Errorf("options = %q", pq.Options)
	}
}

func TestInteractiveBudget_UnderBudgetOffAndAutonomousAreQuiet(t *testing.T) {
	cases := []struct {
		name       string
		budget     int
		ctx        int
		autonomous bool
	}{
		{"under", 150_000, 140_000, false},
		{"off", 0, 400_000, false},
		{"autonomous", 150_000, 400_000, true},
	}
	for _, c := range cases {
		s := newInteractiveBudgetSession(c.budget)
		s.checkInteractiveBudget(lastCall(c.ctx), c.autonomous)
		if s.PendingQuestion() != nil {
			t.Errorf("%s: no question expected", c.name)
		}
	}
}

func TestInteractiveBudget_AsksAgainOnlyAfterHalfABudgetMore(t *testing.T) {
	s := newInteractiveBudgetSession(150_000)
	s.checkInteractiveBudget(lastCall(160_000), false)
	if err := s.AnswerQuestion(s.PendingQuestion().ID, contextRestartOptions[2]); err != nil {
		t.Fatal(err)
	}
	s.checkInteractiveBudget(lastCall(200_000), false)
	if s.PendingQuestion() != nil {
		t.Fatal("asked again too early")
	}
	s.checkInteractiveBudget(lastCall(235_000), false)
	if s.PendingQuestion() == nil {
		t.Fatal("want a second question at +half a budget")
	}
}
