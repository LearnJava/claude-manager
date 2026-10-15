package main

import (
	"fmt"
	"os"

	"claude-manager/internal/control"
)

// genDocs rewrites the generated blocks (tool reference, parity map) of the
// guide at path from the live registry: `cm-mcp -gen-docs [docs/mcp.md]`.
func genDocs(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := control.SyncDocs(string(b))
	if err != nil {
		return err
	}
	if out == string(b) {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// runGenDocs handles `-gen-docs`; it reports whether args asked for it.
func runGenDocs(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "-gen-docs" {
		return false, nil
	}
	path := "docs/mcp.md"
	if len(args) > 1 {
		path = args[1]
	}
	if err := genDocs(path); err != nil {
		return true, fmt.Errorf("gen-docs: %w", err)
	}
	return true, nil
}
