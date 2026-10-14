package cmdguard

import "testing"

func TestProtectedConfigWrite(t *testing.T) {
	deny := []string{
		// The ARB case: a Chat wiring a queue into the project's overlay.
		"cd /c/proj/.claude-manager && cp config.toml .config.toml.bak; python - <<'EOF'\np='config.toml'; s=open(p).read()\nopen(p,'w').write(s)\nEOF",
		`sed -i 's/task_source = ""/task_source = "Q.md"/' .claude-manager/config.toml`,
		`echo 'x = 1' >> ~/.claude-manager/config.toml`,
		`printf 'x' > "C:/proj/.claude-manager/config.local.toml"`,
		`powershell -Command "Set-Content C:\proj\.claude-manager\config.toml 'x'"`,
	}
	allow := []string{
		`cat .claude-manager/config.toml`,
		`grep -n task_source ~/.claude-manager/config.toml | head`,
		`sed -n 1,40p .claude-manager/config.toml`,
		`ls .claude-manager/logs > .tmp/list.txt`,
		`sed -i 's/a/b/' config.toml`, // some other project's config.toml
		`cp .claude-manager/STATUS-P1.md /tmp/q.md`,
	}
	for _, c := range deny {
		if !ProtectedConfigWrite(c) {
			t.Errorf("not denied: %s", c)
		}
	}
	for _, c := range allow {
		if ProtectedConfigWrite(c) {
			t.Errorf("denied: %s", c)
		}
	}
}
