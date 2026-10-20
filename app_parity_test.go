package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"claude-manager/internal/control"
)

// parityExcluded lists exported *App methods that deliberately have no MCP
// tool, each with the reason. Adding a method here is a decision: say why an
// agent does not need it.
var parityExcluded = map[string]string{
	"UpdateConfig": "заменён `update_global_settings`: тот правит отдельные поля под замком сервера с `dry_run` и `version`, а не подменяет весь конфиг",
	"GetProjectCost": "заменён `get_cost` (scope `project`): `GetProjectTokens` отдаёт и стоимость, и токены",
	"GetConfig": "заменён `get_config`: тот читает `GetConfigView` — вид config.toml без секретов и с версией; сырой `GetConfig` остаётся admin-вызовом GUI-харнесса",
	"PickDirectory": "нативный диалог ОС; заменён аргументом `path` у `add_project`/`import_project_logs`",
	"ShowMainWindow": "оконное действие ОС: у агента нет окна",
	"MinimizeToTray": "оконное действие ОС: у агента нет окна",
	"RestartSessionMode": "вариант `RestartSession` для диалога GUI «продолжить/новая»; агент выбирает то же через `restart_session` (`resume`)",
	"Notify":"тост ОС для человека; агент отвечает через результат инструмента",
}

// parityPending lists *App methods whose MCP tool is scheduled in a later
// task of MCP-TASKS.md (value = that task). The list only shrinks: once a
// tool covers a method it must leave this map, and the test fails until it does.
var parityPending = map[string]string{
}

// TestMCPParity: every exported *App method (the Wails bindings = every GUI
// action) is reachable through an MCP tool of the registry, or is excluded
// with a reason, or is pending a scheduled task. A new App method without any
// of these fails the gate - add an MCP tool (internal/control/mcptools.go) or
// an entry in parityExcluded.
func TestMCPParity(t *testing.T) {
	covered := map[string]string{}
	for _, tool := range control.MCPTools {
		for _, m := range tool.Covers {
			covered[m] = tool.Name
		}
	}

	var methods []string
	typ := reflect.TypeOf(&App{})
	for i := 0; i < typ.NumMethod(); i++ {
		methods = append(methods, typ.Method(i).Name)
	}
	sort.Strings(methods)
	known := map[string]bool{}

	for _, m := range methods {
		known[m] = true
		tool, isCovered := covered[m]
		reason, isExcluded := parityExcluded[m]
		task, isPending := parityPending[m]
		switch {
		case isCovered && isPending:
			t.Errorf("%s is covered by tool %s: remove it from parityPending (%s)", m, tool, task)
		case isCovered && isExcluded:
			t.Errorf("%s is covered by tool %s: remove it from parityExcluded", m, tool)
		case isExcluded && isPending:
			t.Errorf("%s is in both parityExcluded and parityPending", m)
		case isExcluded && strings.TrimSpace(reason) == "":
			t.Errorf("%s: parityExcluded needs a non-empty reason", m)
		case isPending && strings.TrimSpace(task) == "":
			t.Errorf("%s: parityPending needs the task that will cover it", m)
		case !isCovered && !isExcluded && !isPending:
			t.Errorf("App.%s has no MCP tool: add one to the registry (internal/control/mcptools.go) or list it in parityExcluded with a reason", m)
		}
	}

	// Stale entries: names that are not (or no longer) App methods.
	for m := range covered {
		if !known[m] {
			t.Errorf("tool %s covers %q, which is not an exported App method", covered[m], m)
		}
	}
	for m := range parityExcluded {
		if !known[m] {
			t.Errorf("parityExcluded lists %q, which is not an exported App method", m)
		}
	}
	for m := range parityPending {
		if !known[m] {
			t.Errorf("parityPending lists %q, which is not an exported App method", m)
		}
	}
}
