package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoreDoesNotKnowProviderNames is the guard behind GLM-PLAN.md §5 and
// AI-STACK III.2 (first step of the general "the core does not know runtime
// names" guard): no non-test file in this package may mention a provider by
// name. Providers are config + app-layer concepts; the core receives only
// abstract artifacts (settings file, env pairs). A failure here means
// provider specifics leaked into the core — move them up a layer.
func TestCoreDoesNotKnowProviderNames(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no sources found — test must run in the package dir")
	}
	banned := []string{"glm", "z.ai"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		lower := strings.ToLower(string(raw))
		for _, bad := range banned {
			if strings.Contains(lower, bad) {
				t.Errorf("%s: the session core must not mention %q (provider specifics live in internal/config and the app layer)", f, bad)
			}
		}
	}
}
