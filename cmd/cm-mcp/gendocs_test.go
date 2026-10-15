package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/control"
)

const guidePath = "../../docs/mcp.md"

// TestDocsInSync fails when docs/mcp.md differs from the registry; fix with
// `go run ./cmd/cm-mcp -gen-docs`.
func TestDocsInSync(t *testing.T) {
	b, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := control.SyncDocs(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if want != string(b) {
		t.Fatal("docs/mcp.md is out of date with the tool registry: run `go run ./cmd/cm-mcp -gen-docs`")
	}
}

func TestDocsListEveryTool(t *testing.T) {
	ref := control.ToolReferenceMarkdown()
	for _, tl := range control.MCPTools {
		if !strings.Contains(ref, "| `"+tl.Name+"` |") {
			t.Errorf("tool %s missing from the reference", tl.Name)
		}
	}
}

func TestGenDocsRewritesAndIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mcp.md")
	seed := "x\n" + control.DocsToolsBegin + "\nstale\n" + control.DocsToolsEnd + "\n" +
		control.DocsParityBegin + "\n" + control.DocsParityEnd + "\n"
	if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := genDocs(p); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(p)
	if strings.Contains(string(got), "stale") || !strings.Contains(string(got), "`get_sessions`") {
		t.Fatalf("not regenerated:\n%.300s", got)
	}
	again, _ := control.SyncDocs(string(got))
	if again != string(got) {
		t.Fatal("generation is not idempotent")
	}
}

func TestGenDocsMissingMarkers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mcp.md")
	_ = os.WriteFile(p, []byte("no markers"), 0o644)
	if err := genDocs(p); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunGenDocsOnlyOnFlag(t *testing.T) {
	if ok, _ := runGenDocs([]string{"--level", "read"}); ok {
		t.Fatal("must not trigger")
	}
}
