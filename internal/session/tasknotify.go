package session

import (
	"html"
	"regexp"
	"strings"
)

// The CLI announces a finished background task (a Bash call with
// run_in_background, a background agent) by injecting a user-role message
//
//	<task-notification><task-id>…</task-id><status>completed</status>
//	<summary>Background command "…" completed (exit code 0)</summary>…</task-notification>
//
// into the conversation. Nobody typed it, so it must not be rendered as the
// user's prompt; formatTaskNotifications turns it into a readable system entry.

const (
	taskNotifyOpen  = "<task-notification>"
	taskNotifyClose = "</task-notification>"
)

var taskNotifyTagRe = regexp.MustCompile(`<([a-z][a-z0-9_-]*)>`)

// isTaskNotification reports whether a user-turn text is a CLI task notification.
func isTaskNotification(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), taskNotifyOpen)
}

// formatTaskNotifications renders every <task-notification> block in text as
// "Background task <status>: <summary>" followed by its remaining fields, one
// "name: value" per line, in the order the CLI sent them. Text outside the
// blocks is kept as is.
func formatTaskNotifications(text string) string {
	var parts []string
	rest := text
	for {
		start := strings.Index(rest, taskNotifyOpen)
		if start < 0 {
			break
		}
		if pre := strings.TrimSpace(rest[:start]); pre != "" {
			parts = append(parts, pre)
		}
		body := rest[start+len(taskNotifyOpen):]
		end := strings.Index(body, taskNotifyClose)
		if end < 0 {
			rest = ""
			parts = append(parts, formatTaskNotification(body))
			break
		}
		parts = append(parts, formatTaskNotification(body[:end]))
		rest = body[end+len(taskNotifyClose):]
	}
	if tail := strings.TrimSpace(rest); tail != "" {
		parts = append(parts, tail)
	}
	return strings.Join(parts, "\n\n")
}

type taskNotifyField struct{ name, value string }

func formatTaskNotification(body string) string {
	var fields []taskNotifyField
	rest := body
	for {
		loc := taskNotifyTagRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			break
		}
		name := rest[loc[2]:loc[3]]
		after := rest[loc[1]:]
		end := strings.Index(after, "</"+name+">")
		if end < 0 {
			rest = after
			continue
		}
		fields = append(fields, taskNotifyField{name, strings.TrimSpace(html.UnescapeString(after[:end]))})
		rest = after[end+len(name)+3:]
	}

	var status, summary string
	var b strings.Builder
	for _, f := range fields {
		switch f.name {
		case "status":
			status = f.value
		case "summary":
			summary = f.value
		}
	}
	b.WriteString("Background task")
	if status != "" {
		b.WriteString(" " + status)
	}
	if summary != "" {
		b.WriteString(": " + summary)
	}
	for _, f := range fields {
		if f.name == "status" || f.name == "summary" || f.value == "" {
			continue
		}
		if strings.Contains(f.value, "\n") {
			b.WriteString("\n" + f.name + ":\n" + f.value)
		} else {
			b.WriteString("\n" + f.name + ": " + f.value)
		}
	}
	return b.String()
}
