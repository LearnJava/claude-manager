# GUI Test Descriptions

Playwright specs against the running Wails app (`CM_CONTROL=1 wails dev`).  
Test fixtures: `frontend/tests/fixtures.ts` — provides `page` (Playwright) and `ctrl` (control-plane RPC client).

Legend: **✓ exists** = spec already written; **○ missing** = not yet covered.

---

## App.svelte — Shell & Global Keyboard Shortcuts

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| APP-01 | Sidebar resize via drag | Drag divider right by 100px | `sidebarWidth` increases to ~350px, content reflows | ○ |
| APP-02 | Sidebar resize clamped to min | Drag divider far left | Sidebar width stays ≥ 150px | ○ |
| APP-03 | Sidebar resize clamped to max | Drag divider far right | Sidebar width stays ≤ 500px | ○ |
| APP-04 | Ctrl+1 selects first session | Start app, press Ctrl+1 | First session becomes selected, SessionView renders | ○ |
| APP-05 | Ctrl+2..4 cycle sessions | Press Ctrl+2, Ctrl+3, Ctrl+4 | Corresponding session becomes active each time | ○ |
| APP-06 | Ctrl+F focuses log search | Click main area, press Ctrl+F | Search input in LogStream receives focus, text selected | ○ |
| APP-07 | Ctrl+F ignored inside textarea | Focus SessionInput textarea, press Ctrl+F | Browser Find opens instead, log search not focused | ○ |
| APP-08 | Settings modal opens/closes | Click Settings button; press Escape | Modal appears then disappears | ○ |
| APP-09 | History modal opens/closes | Click History button; click backdrop | Modal appears then disappears | ○ |
| APP-10 | Dashboard modal opens/closes | Click Dashboard button; click backdrop | Modal appears then disappears | ○ |
| APP-11 | No session selected placeholder | Open app with no session selected | "Select a session" placeholder visible in main area | ○ |

---

## Sidebar.svelte — Navigation & Session Controls

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| SB-01 | Click session row selects it | Click any session row | `$selectedSessionId` updates, SessionView loads | **✓** |
| SB-02 | Start button fires working event | Hover row, click ▶ | `session:status` event `status=working` within 5s; dot turns green | **✓** |
| SB-03 | Stop button stops session | Start session, click ■ | `session:status` event `status=idle` within 5s | ○ |
| SB-04 | Status dot is idle before start | Load app | Dot has `bg-status-idle` class | **✓** |
| SB-05 | Status dot blinks when waiting_permission | Trigger permission_request scenario | Dot has blinking class | ○ |
| SB-06 | Status dot yellow during rate_limited | Trigger rate_limit_event | Dot has `bg-status-rate-limited` color | ○ |
| SB-07 | Collapse/expand project section | Click project header twice | Session list hides then shows | ○ |
| SB-08 | Start all project sessions | Click ▶ on project header | All sessions in project reach working/starting | ○ |
| SB-09 | Stop all project sessions | Click ■ on project header (all running) | All sessions reach idle | ○ |
| SB-10 | Delete project — first click arms button | Click ✕ once | Button turns red `?`, auto-cancel timer starts | ○ |
| SB-11 | Delete project — second click removes | Click ✕ twice in 3s | Project disappears from list, config updated | ○ |
| SB-12 | Delete project — cancel times out | Click ✕ once, wait 3s | Button reverts to ✕ without deleting | ○ |
| SB-13 | Uptime badge hidden on hover | Hover session row | Uptime element not visible while hovered | ○ |
| SB-14 | ModelPicker shown when auto-routing on | Enable `auto_model_routing`, click ▶ | ModelPicker modal appears before session starts | ○ |
| SB-15 | Model dropdown has one entry per model | Load app (S1 configured as pinned id `claude-sonnet-4-6`) | Options are exactly Haiku/Sonnet/Opus/Fable, value `sonnet` — no duplicate spelling appended | **✓** |

---

## SessionView.svelte — Control Bar & Log Management

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| SV-01 | Stop button terminates session | Start session, click Stop | `session:status` idle; button disabled after | ○ |
| SV-02 | Restart button restarts session | Start session, click Restart | Session reaches working again within 10s | ○ |
| SV-03 | Stop after task triggers soft stop | Start session, click Stop after task | Session completes current task then goes idle | ○ |
| SV-04 | Control buttons disabled when idle | Select idle session | Stop, Stop after task, Restart buttons all disabled | ○ |
| SV-05 | Copy log copies to clipboard | Session with logs, click Copy | Clipboard contains log text; button shows "✓ Copied" for 1.5s | ○ |
| SV-06 | Clear log empties log view | Session with logs, click Clear log | Log container shows empty state; backend history preserved | ○ |
| SV-07 | Export markdown creates file | Select md format, click Export | Success path shown; file exists on disk | ○ |
| SV-08 | Export JSON creates file | Select json format, click Export | Success path shown; file is valid JSON | ○ |
| SV-09 | Export confirmation clears after 3s | Export successfully | Export confirm message gone after 3s | ○ |

---

## SessionInput.svelte — Message Send

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| SI-01 | Send message via button click | Type text, click Send | Backend receives user_message; textarea cleared | **✓** |
| SI-02 | Send message via Enter key | Type text, press Enter | Same as SI-01 | ○ |
| SI-03 | Shift+Enter creates newline | Type text, press Shift+Enter | Textarea has newline; message not sent | ○ |
| SI-04 | Send button disabled when idle | Session idle | Send button disabled; textarea disabled | **✓** |
| SI-05 | Send button disabled for empty input | Session working, empty textarea | Send button disabled | ○ |
| SI-06 | Sending state shown during flight | Intercept RPC, click Send | Button shows "Sending…" while in flight | ○ |
| SI-07 | Error message shown on send failure | Make backend return error, click Send | Error message rendered above input | ○ |

---

## LogStream.svelte — Search & Scroll

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| LS-01 | Search filters log entries | Session with 20 entries, type query matching 3 | Only 3 entries visible; count badge shows "3/20" | ○ |
| LS-02 | Clear search button shows all | After LS-01, click X | All 20 entries visible; badge gone | ○ |
| LS-03 | Escape clears search | After LS-01, press Escape | Filter cleared | ○ |
| LS-04 | Autoscroll on new entry | Scroll to bottom | New entry appended → view auto-scrolls | ○ |
| LS-05 | Autoscroll disabled after manual scroll | Scroll up >60px from bottom | New entry appended → view does NOT scroll | ○ |
| LS-06 | Jump to bottom button appears | Scroll up past threshold | "↓ Jump to latest" button visible | ○ |
| LS-07 | Jump to bottom button scrolls down | Click "↓ Jump to latest" | Scroll position at bottom; button hidden | ○ |
| LS-08 | "No entries match" shown when filter misses | Type query matching nothing | Empty state message "No entries match query" visible | ○ |
| LS-09 | "No log entries yet" on fresh session | Select session with empty log | Empty state message "No log entries yet" visible | ○ |
| LS-10 | Ctrl+F focuses and selects search text | Ctrl+F with text already in search | Text is selected in input (ready to replace) | ○ |
| LS-11 | Markdown message renders formatted | Push a log entry with headings/list/table/code | `.md-body` with `<h2>/<li>/<td>/<pre>` rendered, link has `href` | ✓ |
| LS-12 | Markdown checkbox switches to raw | Uncheck "Markdown" | `.md-body` gone, `**bold**` visible verbatim | ✓ |
| LS-13 | Markdown entry starts expanded | Push a long markdown entry | Full body shown, not the one-line summary | ✓ |
| LS-14 | Plain text unaffected by the toggle | Push `Working on the task...` | Text rendered as before, no `.md-body` | ✓ |
| LS-15 | Tool output with a document renders by default | Push a `tool_result` with `- **bold**` list | `.md-body` with 2 `<li>`; `M` button lit, click → raw, click → back | ✓ |
| LS-17 | Machine output stays raw, button offers it | Push a `tool_result` with Read line numbers | No `.md-body`; clicking `M` renders that row | ✓ |
| LS-18 | Prose rows have no per-entry button | Push a `text` entry with markdown | Rendered, but no `M` button | ✓ |
| LS-16 | Markdown choice persists | Uncheck, reload the app | Checkbox still unchecked (localStorage) | ✓ |

---

## SessionCard.svelte — KPI Display

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| SC-01 | Runtime increments every second | Select running session | Runtime display increases by 1s each second | ○ |
| SC-02 | Context bar fills and turns yellow at 60% | Push session to 60% context use | Context bar has yellow color class | ○ |
| SC-03 | Context bar turns red at 80% | Push session to 80% context use | Context bar has red color class | ○ |
| SC-04 | Cache hit ratio shown correctly | Session with cache events | Displayed % matches `cache_read / (cache_read + cache_creation)` | ○ |
| SC-05 | Branch shown if present | Session has branch in metadata | Branch label visible in card row 2 | ○ |
| SC-06 | Model shown if present | Session has model metadata | Model label visible in card row 2 | ○ |

---

## ModelPicker.svelte — Pre-flight Model Selection

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| MP-01 | Loading state shown during analysis | Enable auto-routing, click ▶ | "Analyzing task complexity…" visible | ○ |
| MP-02 | Recommendation displayed | Analysis returns valid plan | Complexity, files, tokens, confidence, reasoning shown | ○ |
| MP-03 | Accept recommendation starts session | Click Start (no override) | Session starts with recommended model/effort | ○ |
| MP-04 | Override expands panel | Click "Override model / effort" | Dropdowns for model and effort become visible | ○ |
| MP-05 | Override model and start | Select haiku, click Start | Session starts with haiku model regardless of recommendation | ○ |
| MP-06 | Cancel closes modal | Click Cancel | Modal disappears; no session started | ○ |
| MP-07 | Escape closes modal | Press Escape | Same as MP-06 | ○ |
| MP-08 | Error state shown on analysis failure | Backend returns error | Error message visible in modal | ○ |

---

## PermissionBanner.svelte — Inline Permission

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| PB-01 | Banner appears on permission request | Trigger permission scenario | Banner with "waiting for permission" visible | **✓** |
| PB-02 | Tool name shown in banner | Same as above | Tool name rendered in banner | **✓** |
| PB-03 | Allow resolves permission | Click ✓ Allow | Banner disappears; session status returns to working | **✓** |
| PB-04 | Deny resolves permission | Click ✗ Deny | Banner disappears | **✓** |
| PB-05 | Allow similar sends allow_similar | Click ✓ Allow similar | Backend receives `allow_similar` decision | ○ |
| PB-06 | Always allow adds rule | Click ✓ Always allow | Backend receives `allow_always`; rule added to config | ○ |
| PB-07 | Always deny adds rule | Click ✗ Always deny | Backend receives `deny_always` | ○ |
| PB-08 | Buttons disabled during response flight | Click Allow, intercept RPC | Other buttons disabled until response resolves | ○ |
| PB-09 | Flash at 30s of waiting | Let permission wait 30s | Banner pulses golden | ○ |
| PB-10 | Risk level high → red border | High-risk permission | Banner has red left border | ○ |
| PB-11 | Risk level low → green border | Low-risk permission | Banner has green left border | ○ |
| PB-12 | Wait time display increments | Let banner sit | Wait time increases each second | ○ |

---

## PermissionQueue.svelte — Batch Permissions

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| PQ-01 | Queue shows all pending permissions | Trigger 2 sessions waiting | Modal header shows "(2 pending)" | ○ |
| PQ-02 | Per-row Allow works | Click Allow on one row | That row disappears; others remain | ○ |
| PQ-03 | Per-row Deny works | Click Deny on one row | That row disappears | ○ |
| PQ-04 | Allow all safe skips high-risk | 2 low + 1 high-risk pending | Click "Allow all safe" → 2 resolve; high-risk stays | ○ |
| PQ-05 | Deny all resolves all | Click "Deny all" | All rows disappear | ○ |
| PQ-06 | Allow all safe disabled with 0 safe | Only high-risk entries | "Allow all safe (0)" button disabled | ○ |
| PQ-07 | StatusBar "Waiting" click opens queue | Sessions with pending permissions | Click "Waiting: N" in status bar → queue modal opens | ○ |
| PQ-08 | Bulk buttons disabled during operation | Click "Deny all", intercept RPC | Per-row buttons disabled until operation completes | ○ |

---

## CostDashboard.svelte — Analytics

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| CD-01 | Opens with Today selected | Click Dashboard | "Today" period button active; KPIs shown | ○ |
| CD-02 | Period switch to This week | Click "This week" | Data reloads; "This week" button active | ○ |
| CD-03 | Period switch to This month | Click "This month" | Data reloads | ○ |
| CD-04 | Refresh reloads data | Click Refresh | Loading indicator shown; data updated | ○ |
| CD-05 | No runs empty state | No sessions run today | "No runs in selected period" message shown | ○ |
| CD-06 | Cache efficiency calculated | Sessions with cache data | Efficiency = `cache_read / (cache_read + cache_creation)` | ○ |
| CD-07 | Rate limit red when >85% | RL utilization > 85% | RL KPI card has red color | ○ |
| CD-08 | Cost by project bar chart rendered | Multiple projects with cost | Bar chart with project labels visible | ○ |
| CD-09 | Unit switch flips every chart | Click "Tokens" / "USD" | Headings read "By model — tokens"/"— cost"; KPI + bars reformat, bar order re-sorts by the active unit | ○ |
| CD-10 | Unit choice persists | Pick USD, reload | "USD" still active (localStorage `cm.costUnit`) | ○ |
| CD-11 | Bar tooltip always shows both units | Hover a project bar | `title` has `<N> tok · $<X>` regardless of active unit | ○ |

---

## History.svelte — Session Run Table

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| HI-01 | Runs shown in table | Complete a session | Run appears in History table | ○ |
| HI-02 | Sort by started descending by default | Open History | Newest run at top | ○ |
| HI-03 | Click column header sorts ascending | Click "Cost" header | Cheapest run at top; ▲ indicator shown | ○ |
| HI-04 | Second click same column reverses sort | Click "Cost" again | Most expensive at top; ▼ indicator shown | ○ |
| HI-05 | Project filter | Select project from dropdown | Only runs for that project visible; count badge updates | ○ |
| HI-06 | Session name text filter | Type partial name | Matching rows visible only | ○ |
| HI-07 | Status filter | Select "error" from status dropdown | Only error runs shown | ○ |
| HI-08 | Date range from filter | Set From date to yesterday | Runs before yesterday hidden | ○ |
| HI-09 | Date range to filter | Set To date to yesterday | Runs after yesterday hidden | ○ |
| HI-10 | Clear resets all filters | Set multiple filters, click Clear | All runs visible again; inputs cleared | ○ |
| HI-11 | Row expand shows log entries | Click any row | Expanded view shows log entries | ○ |
| HI-12 | Row expand shows token breakdown | Click any row with token data | Input/output/cache token counts shown | ○ |
| HI-13 | Collapse row on second click | Click expanded row again | Log viewer hidden | ○ |
| HI-14 | Log loading state shown | Click row, intercept RPC | "Loading logs…" visible during fetch | ○ |
| HI-15 | Log load error shown | Backend returns error for logs | Error message in expanded row | ○ |

---

## PlanReview.svelte — Task Plan Editing & Execution

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| PR-01 | Feasibility section shown | Open PlanReview with valid plan | Complexity bar, files, tokens, confidence, reasoning visible | ○ |
| PR-02 | Risks list shown if non-empty | Plan has risks | Risk items visible below feasibility | ○ |
| PR-03 | Edit mode enables add/remove/reorder | Click "Edit plan" | Add step, ✕, ↑↓ arrows appear | ○ |
| PR-04 | Add step creates new subtask | Click + Add step | New subtask card appears at end | ○ |
| PR-05 | Remove step deletes subtask | Click ✕ on a subtask | Subtask removed; count decreases | ○ |
| PR-06 | Move step up reorders | Click ↑ on second subtask | Steps swap order | ○ |
| PR-07 | Edit subtask form opens | Click Edit on subtask | Form fields appear (name, model, prompt, etc.) | ○ |
| PR-08 | Edit subtask saves on Done | Edit name, click Done | Subtask shows new name | ○ |
| PR-09 | Save plan calls ApprovePlan | Edit, click Save plan | Backend ApprovePlan called; edit mode exits | ○ |
| PR-10 | Done editing exits without save | Edit, click "Done editing" | Changes discarded; edit mode exits | ○ |
| PR-11 | Execute plan calls ExecutePlan | Click "Execute plan" | Backend ExecutePlan called; `working` state shown | ○ |
| PR-12 | Re-analyze dispatches event | Click Re-analyze | Parent receives reanalyze event | ○ |
| PR-13 | Cancel dispatches event | Click Cancel | Parent receives cancel event | ○ |
| PR-14 | Estimated totals shown | Plan with multiple subtasks | Total cost/time/tokens visible in footer | ○ |

---

## Settings.svelte — Configuration

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| ST-01 | Open modal shows Global tab | Click Settings | Global tab active; claude_path field visible | **✓** |
| ST-02 | Switch to Projects tab | Click Projects tab | Project list visible | **✓** |
| ST-03 | Switch to Sessions tab | Click Sessions tab | Session editor visible | **✓** |
| ST-04 | Edit model and save persists | Open Sessions, change model to haiku, save | GetConfig returns session.Model = "haiku" | **✓** |
| ST-05 | "Saved." message appears | Click Save | Success message shown in footer | **✓** |
| ST-06 | Add project creates new entry | Click Add project | New blank project card appears | ○ |
| ST-07 | Browse sets project path | Click Browse, pick folder | Path input updated with selected path | ○ |
| ST-08 | Remove project removes card | Click Remove on a project | Project card removed from list | ○ |
| ST-09 | Add session creates new entry | Click Add session | New blank session appears in left rail | ○ |
| ST-10 | Add permission rule adds row | Click + Add rule in Sessions tab | New rule row with Tool/Pattern/Decision appears | ○ |
| ST-11 | Remove permission rule removes row | Click ✕ on rule | Rule row gone | ○ |
| ST-12 | Auto-model-routing toggle saves | Toggle checkbox, save | GetConfig returns updated `auto_model_routing` | ○ |
| ST-13 | Crash recovery toggle saves | Toggle checkbox, save | GetConfig reflects change | ○ |
| ST-14 | Theme change applies immediately | Change theme dropdown | App theme changes without Save | ○ |
| ST-15 | Close without save discards | Edit field, click Close | Config not updated; original value persists | ○ |
| ST-16 | Error state on config load failure | Backend returns error | "No config loaded" error visible | ○ |
| ST-17 | Workers tab: add preset persists | Open Workers, add Step 3.7 preset, save | GetConfig returns worker step37 with the preset model | **✓** |
| ST-18 | Project mixed-programming opt-in | Projects tab, enable mixed on a project | Privacy warning + Gates textarea appear | ○ |
| ST-19 | Project logs panel shows count/size | Projects tab, project with saved logs | "Saved logs: N files, X KB/MB" shown | ○ |
| ST-20 | Clear project logs (two-click confirm) | Click "Clear project logs" twice | Files removed from disk; count resets to 0 files | ○ |

---

## MixedRun.svelte — Mixed Programming (MP-08)

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| MX-01 | Modal opens from header | Click Mixed | Modal titled "Mixed programming" appears | **✓** |
| MX-02 | Mixed project/worker preselected | Open modal | Project select = mixedproj, worker select = fake | **✓** |
| MX-03 | Dispatch runs a round | Enter brief, click Dispatch | `worker:done` `status=done, rounds=1`; task timeline shows "done" and "Round 1" | **✓** |
| MX-04 | Quality table populates | After a completed dispatch | Quality table lists the worker with done/total counts | **✓** |
| MX-05 | Empty state when no mixed project | Open modal with mixed disabled everywhere | "No project has mixed programming enabled" message | ○ |
| MX-06 | Cancel a running task | Dispatch a slow task, click Cancel | Task reaches needs_human; CancelMixedTask called | ○ |
| MX-07 | Gate output expand/collapse | Task with gate failure, click "gates" | Gate stdout/stderr shown, toggles off on re-click | ○ |

---

## StatusBar.svelte — Footer Metrics

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| STB-01 | Active session count updates | Start a session | "Active: 1/N" count shown | ○ |
| STB-02 | Waiting count shown | Session waits for permission | "Waiting: 1" colored entry visible | ○ |
| STB-03 | Click Waiting opens queue modal | Click "Waiting: N" | PermissionQueue modal appears | ○ |
| STB-04 | Error count shown | Session errors | "Errors: 1" colored entry visible | ○ |
| STB-05 | Today's usage shown | Complete session with cost | "Today: <N> tok ($X.XX)" shown, tokens leading | ○ |
| STB-05b | Clicking the total switches units | Click the "Today:" readout | Dollars become the headline, tokens move to parentheses; choice persists | ○ |
| STB-06 | Rate limit shown red >85% | RL utilization >85% | "RL:" value in red | ○ |
| STB-07 | Uptime increments | Wait 2 seconds | Uptime increases by ~2s | ○ |

---

## AlienCrew.svelte — Animated Status Display

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| AC-01 | Demo cycles all states | Click demo button | States cycle every ~1.8s; all 10 activities shown | ○ |
| AC-02 | Error state on session error | Session reaches error status | Error prop animation plays; characters look alarmed | ○ |
| AC-03 | Waiting state on permission | Session waiting_permission | Hourglass animation plays | ○ |
| AC-04 | Sleeping state on rate limit | Session rate_limited | Lantern/sleeping animation; Z's shown | ○ |
| AC-05 | Working state on active session | Session working | Appropriate tool animation based on active tool | ○ |
| AC-06 | Idle state with no sessions | All sessions idle | Spinning wheel animation (default idle prop) | ○ |

---

## Cross-Cutting & Edge Cases

| ID | Title | Steps | Assert | Status |
|----|-------|-------|--------|--------|
| CC-01 | Rate limit banner shown and countdown | Trigger rate_limit_event | RateLimitBanner appears with countdown | ○ |
| CC-02 | Rate limit banner clears on resume | RL timer expires | Banner disappears; session resumes | ○ |
| CC-03 | Session auto-restarts on context >75% | Push context to 75% with fakeclaude | Session restarts automatically | ○ |
| CC-04 | Crash recovery on restart | Kill app mid-session, restart | Session resumes with `--resume` flag | ○ |
| CC-05 | Clear session state works | Click ClearSessionState, restart | Session starts fresh (no `--resume`) | ○ |
| CC-09 | Unfinished run asks before resuming | Click ▶ on a session whose state file has a `session_id` | ResumePrompt shows the interrupted task; nothing starts until a choice is made | ✓ `resume.spec.ts` |
| CC-10 | Continue vs Start fresh | Pick each button in ResumePrompt | Continue → StartSession only; Start fresh → ClearSessionState then StartSession | ✓ `resume.spec.ts` |
| CC-11 | Unfinished marker in sidebar | Session idle with a saved state file | ⏸ marker on the session row, gone once it runs / completes | ✓ `resume.spec.ts` |
| CC-06 | Keyboard shortcuts inactive in modal | Open Settings, press Ctrl+1 | Session does not change | ○ |
| CC-07 | Multiple concurrent permissions | 3 sessions all waiting | Queue shows 3 entries; each can be approved independently | ○ |
| CC-08 | Session with all statuses via fakeclaude | Load `all_statuses` scenario | Status dot cycles through all 6 colors | ○ |

---

## Implementation Notes

- **Fixtures:** `frontend/tests/fixtures.ts` — import `ctrl` for RPC, `page` for DOM
- **Fakeclaude scenarios:** `testdata/scenarios/` — use `FAKECLAUDE_SCENARIO` env var or `claude_path` in config
- **Control-plane base URL:** `http://127.0.0.1:7333`
- **Start command:** `CM_CONTROL=1 wails dev` (GUI) or `./build/playwright-server.exe` (headless)
- **RPC helpers available:** `ctrl.rpc('StartSession', ...)`, `ctrl.wait('session:status', ...)`
- **Existing specs:** `frontend/tests/{permission,session,settings,sidebar}.spec.ts`
