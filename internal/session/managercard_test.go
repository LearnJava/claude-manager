package session

import (
	"strings"
	"testing"

	"claude-manager/internal/config"
)

func TestManagerCard(t *testing.T) {
	sessions := []config.SessionConfig{
		{Name: "Chat"},
		{Name: "Developer 1", Prompt: "Ты программист на Java,\n используешь Spring.", TaskSource: "STATUS-P1.md", AutoRestart: true},
	}

	chat := managerCard("ARB", `C:\proj\arb`, sessions, "Chat")
	for _, want := range []string{
		"## Claude Manager",
		"not people",
		"- `Chat` (you) — no queue (interactive)",
		"- `Developer 1` — queue `C:/proj/arb/STATUS-P1.md`, takes tasks one by one unattended; role: \"Ты программист на Java, используешь Spring.\"",
		"`C:/proj/arb/<BLOCK>-TASKS.md`",
		"Done when",
		// STATUS-P1.md is taken: a queue-less session is pointed at the next free name.
		"Task source in Settings → Sessions to `STATUS-P2.md`",
		"Never edit the manager's configuration",
	} {
		if !strings.Contains(chat, want) {
			t.Errorf("chat card lacks %q:\n%s", want, chat)
		}
	}
	if strings.Contains(chat, "### Your queue") {
		t.Errorf("a session without a queue got the queue section:\n%s", chat)
	}
	if again := managerCard("ARB", `C:\proj\arb`, sessions, "Chat"); again != chat {
		t.Error("card is not byte-stable across calls")
	}

	dev := managerCard("ARB", `C:\proj\arb`, sessions, "Developer 1")
	if !strings.Contains(dev, "### Your queue\nYour queue is `C:/proj/arb/STATUS-P1.md`") {
		t.Errorf("developer card lacks its own queue:\n%s", dev)
	}
	if strings.HasSuffix(dev, "\n") {
		t.Error("card ends with a newline")
	}
}

func TestManagerCardFor_Off(t *testing.T) {
	m := &SessionManager{cfg: &config.AppConfig{}}
	if got := m.managerCardFor(&config.ProjectConfig{Name: "p", Sessions: []config.SessionConfig{{Name: "s"}}}, "s"); got != "" {
		t.Errorf("manager_card off must add nothing, got %q", got)
	}
}

func TestNextQueueName(t *testing.T) {
	for _, tc := range []struct {
		sources []string
		want    string
	}{
		{nil, "STATUS-P1.md"},
		{[]string{"", "STATUS-P1.md"}, "STATUS-P2.md"},
		{[]string{`C:\proj\status-p1.md`, "docs/STATUS-P2.md"}, "STATUS-P3.md"},
		{[]string{".claude-manager/STATUS-DEV1.md"}, "STATUS-P1.md"},
	} {
		var ss []config.SessionConfig
		for _, q := range tc.sources {
			ss = append(ss, config.SessionConfig{TaskSource: q})
		}
		if got := nextQueueName(ss); got != tc.want {
			t.Errorf("nextQueueName(%v) = %q, want %q", tc.sources, got, tc.want)
		}
	}
}
