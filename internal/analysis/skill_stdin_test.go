package analysis

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// TestMain lets this test binary stand in for the claude CLI: with
// CM_HELPER_CLAUDE=1 it reads the prompt from stdin, checks it arrived whole
// and prints one result event carrying a valid skill draft.
func TestMain(m *testing.M) {
	if os.Getenv("CM_HELPER_CLAUDE") == "1" {
		os.Exit(helperClaude())
	}
	os.Exit(m.Run())
}

func helperClaude() int {
	for _, a := range os.Args[1:] {
		if len(a) > 20000 {
			fmt.Fprintln(os.Stderr, "helper: oversized argv entry")
			return 3
		}
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper: read stdin:", err)
		return 2
	}
	prompt := string(data)
	if !strings.HasPrefix(prompt, "Distill this recurring pattern into a skill:") ||
		!strings.Contains(prompt, "LONGMARK") || len(prompt) < 100000 {
		fmt.Fprintf(os.Stderr, "helper: prompt wrong, len=%d\n", len(prompt))
		return 4
	}
	fmt.Println(`{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.01,` +
		`"result":{"name":"long-prompt","description":"d","steps":[{"command":"x"}],"done_when":"ok"}}`)
	return 0
}

func TestDistillSkill_LongPromptViaStdin(t *testing.T) {
	t.Setenv("CM_HELPER_CLAUDE", "1")
	long := "LONGMARK" + strings.Repeat("x", 100000)
	d, err := DistillSkill(context.Background(), t.TempDir(),
		SkillDistillInput{
			Sig:     []string{"Bash:long"},
			Samples: []SkillSample{{Command: long}},
		}, 100, 1, AnalysisConfig{ClaudePath: os.Args[0]}, nil)
	if err != nil {
		t.Fatalf("DistillSkill: %v", err)
	}
	if d.Name != "long-prompt" {
		t.Errorf("draft name = %q", d.Name)
	}
}
