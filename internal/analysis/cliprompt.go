package analysis

import (
	"os/exec"
	"strings"
)

// setPromptStdin feeds the prompt of a one-shot `claude -p` call through stdin
// instead of the last command-line argument. A Windows command line is capped
// at 32,767 characters, which long skill patterns, journals and handoffs
// exceed; `claude -p` without a positional prompt reads it from stdin.
func setPromptStdin(cmd *exec.Cmd, prompt string) {
	cmd.Stdin = strings.NewReader(prompt)
}
