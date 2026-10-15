package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// solutions are the scripted agents for the offline test: the shortest tool
// sequence that does each task, returning the ANSWER where the task has one.
var solutions = map[string]func(st *Steps, in *Instance) string{
	"setup-from-scratch": func(st *Steps, in *Instance) string {
		st.Do("add_project", map[string]any{"name": "notes", "path": filepath.Join(in.Dir, "notes-folder")})
		st.Do("add_session", map[string]any{"project": "notes", "name": "Writer", "fields": map[string]any{
			"prompt": "simple hello task", "model": "claude-sonnet-4-6", "permission_mode": "acceptEdits"}})
		st.Do("start_session", map[string]any{"session_id": "notes/Writer"})
		time.Sleep(time.Second)
		st.Poll("get_session", map[string]any{"session_id": "notes/Writer"}, func(o map[string]any) bool { return sessionStatus(o) == "idle" })
		return ""
	},
	"run-and-report-cost": func(st *Steps, in *Instance) string {
		st.Do("start_session", map[string]any{"session_id": "shop/Build"})
		time.Sleep(time.Second)
		st.Poll("get_session", map[string]any{"session_id": "shop/Build"}, func(o map[string]any) bool { return sessionStatus(o) == "idle" })
		m := st.Do("get_session_metrics", map[string]any{"session_id": "shop/Build"})
		return findNumber(m, "total_cost_usd", "cost_usd")
	},
	"answer-question": func(st *Steps, in *Instance) string {
		q := st.Do("get_pending_questions", map[string]any{"session_id": "plan/Planner"})
		st.Do("answer_question", map[string]any{"session_id": "plan/Planner", "question_id": findString(q, "id"), "answer": "Relax to 16ms"})
		return ""
	},
	"approve-one-permission": func(st *Steps, in *Instance) string {
		p := st.Do("get_pending_permissions", nil)
		for _, r := range asMaps(p["permissions"]) {
			id, _ := r["session_id"].(string)
			rid, _ := r["id"].(string)
			if id == "ops/A" {
				st.Do("approve_permission", map[string]any{"session_id": id, "request_id": rid})
			} else {
				st.Do("deny_permission", map[string]any{"session_id": id, "request_id": rid})
			}
		}
		return ""
	},
	"switch-model-and-restart": func(st *Steps, in *Instance) string {
		st.Do("set_session_model", map[string]any{"session_id": "dev/Worker", "model": "claude-opus-4-6"})
		st.Do("restart_session", map[string]any{"session_id": "dev/Worker"})
		return ""
	},
	"stop-project": func(st *Steps, in *Instance) string {
		st.Do("stop_project", map[string]any{"project": "dock"})
		return ""
	},
	"update-session-config": func(st *Steps, in *Instance) string {
		st.Do("update_session", map[string]any{"session_id": "ops2/Nightly", "fields": map[string]any{
			"model": "claude-haiku-4-5", "permission_mode": "acceptEdits", "max_budget_usd": 2.5}})
		return ""
	},
	"remove-project": func(st *Steps, in *Instance) string {
		st.Do("remove_project", map[string]any{"project": "old"})
		return ""
	},
	"find-failed-session": func(st *Steps, in *Instance) string {
		st.Do("get_sessions", nil)
		l := st.Do("get_session_logs", map[string]any{"session_id": "ci/Test", "tail": 20})
		if raw, _ := json.Marshal(l); strings.Contains(string(raw), "users_email_key") {
			return "Test/users_email_key"
		}
		return ""
	},
	"project-cost": func(st *Steps, in *Instance) string {
		c := st.Do("get_cost", map[string]any{"scope": "project", "project": "fin", "period": 7})
		return findNumber(c, "cost_usd")
	},
	"generate-and-approve-roadmap": func(st *Steps, in *Instance) string {
		g := st.Do("generate_roadmap", map[string]any{"project": "books", "idea": "eval-roadmap: a bookmark manager"})
		st.Do("approve_roadmap", map[string]any{"plan_id": planID(g)})
		r := st.Do("get_session_roadmap", map[string]any{"session_id": "books/P1"})
		return fmt.Sprint(r["total"])
	},
	"skill-candidates": func(st *Steps, in *Instance) string {
		c := st.Do("get_skill_candidates", map[string]any{"project": "sk"})
		list := asMaps(c["candidates"])
		if len(list) == 0 {
			return ""
		}
		var sigs []string
		for _, s := range asList(list[0]["sig"]) {
			sigs = append(sigs, fmt.Sprint(s))
		}
		return strings.Join(sigs, " > ")
	},
	"wait-for-permission": func(st *Steps, in *Instance) string {
		st.Do("start_project", map[string]any{"project": "wa"})
		st.Do("wait_for_any", map[string]any{"session_ids": []string{"wa/Alpha", "wa/Beta"}, "timeout_ms": 15000})
		p := st.Do("get_pending_permissions", nil)
		for _, r := range asMaps(p["permissions"]) {
			return fmt.Sprintf("Alpha/%v", r["tool"])
		}
		return ""
	},
}

func asList(v any) []any { l, _ := v.([]any); return l }

func asMaps(v any) []map[string]any {
	var out []map[string]any
	for _, x := range asList(v) {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// walk visits every key/value of a decoded JSON tree, depth first, until visit
// returns true.
func walk(v any, visit func(key string, val any) bool) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if visit(k, x) || walk(x, visit) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if walk(x, visit) {
				return true
			}
		}
	}
	return false
}

// findString returns the first string value stored under key anywhere in v.
func findString(v any, key string) string {
	out := ""
	walk(v, func(k string, x any) bool {
		if s, ok := x.(string); ok && k == key {
			out = s
			return true
		}
		return false
	})
	return out
}

// findNumber returns the first number stored under one of keys, as text.
func findNumber(v any, keys ...string) string {
	out := ""
	walk(v, func(k string, x any) bool {
		for _, want := range keys {
			if k != want {
				continue
			}
			if f, ok := x.(float64); ok {
				out = fmt.Sprint(f)
				return true
			}
			if s, ok := x.(string); ok && s != "" {
				out = s
				return true
			}
		}
		return false
	})
	return out
}

// planID reads the top-level id of the plan in a generate_roadmap result.
func planID(out map[string]any) string {
	plan, _ := out["plan"].(map[string]any)
	return fmt.Sprint(plan["id"])
}
