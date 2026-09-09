/**
 * Wails bridge mock for Playwright.
 *
 * Injects window.go.main.App.* and window.runtime.* stubs into every page so
 * the Svelte app works against the control-plane HTTP/WS instead of the native
 * Wails WebView2 runtime.
 *
 * RPC calls go directly to http://127.0.0.1:<port>/rpc via fetch (CORS headers
 * are added by the control-plane's tokenAuth middleware).
 *
 * Events arrive via a WebSocket to ws://127.0.0.1:<port>/events?token=… (the
 * server accepts the token as a query parameter for WS because browsers cannot
 * set custom headers during the upgrade).
 *
 * Usage in a Playwright fixture:
 *
 *   await installBridge(page, port, token);
 *   await page.goto('/');
 */

import type { Page } from '@playwright/test';

/** Install the bridge on a Playwright page before navigation. */
export async function installBridge(page: Page, port: string, token: string): Promise<void> {
  await page.addInitScript(
    // This function is serialised and runs inside the browser before any app
    // code.  Keep it self-contained (no imports, no closures over outer vars).
    ({ p, t }: { p: string; t: string }) => {
      const base = `http://127.0.0.1:${p}`;

      // ── RPC helper ──────────────────────────────────────────────────────────
      function rpc<T>(method: string, params: Record<string, unknown>): Promise<T> {
        return fetch(`${base}/rpc`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-CM-Token': t },
          body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
        })
          .then((r) => r.json())
          .then((body: { result?: T; error?: { message: string } }) => {
            if (body.error) throw new Error(body.error.message);
            return body.result as T;
          });
      }

      // ── Event registry ──────────────────────────────────────────────────────
      // map: eventName → [[callback, remaining]]  (remaining = -1 for infinite)
      const _cbs: Record<string, Array<[(data: unknown) => void, number]>> = {};

      (window as typeof window & { __dispatchWailsEvent: (n: string, d: unknown) => void })
        .__dispatchWailsEvent = function (name: string, data: unknown) {
          const entries = _cbs[name];
          if (!entries || entries.length === 0) return;
          const kept: Array<[(data: unknown) => void, number]> = [];
          for (const [cb, rem] of entries) {
            cb(data);
            if (rem === -1 || rem > 1) kept.push([cb, rem === -1 ? -1 : rem - 1]);
          }
          _cbs[name] = kept;
        };

      // ── window.runtime stub ─────────────────────────────────────────────────
      (window as typeof window & { runtime: unknown }).runtime = {
        EventsOnMultiple(name: string, cb: (d: unknown) => void, max: number) {
          if (!_cbs[name]) _cbs[name] = [];
          _cbs[name].push([cb, max]);
        },
        EventsOff(...names: string[]) {
          names.forEach((n) => { delete _cbs[n]; });
        },
        EventsOffAll() { Object.keys(_cbs).forEach((k) => { delete _cbs[k]; }); },
        EventsEmit() { /* no-op */ },
        LogPrint() {}, LogTrace() {}, LogDebug() {}, LogInfo() {},
        LogWarning() {}, LogError() {}, LogFatal() {},
        WindowReload() {}, WindowCenter() {}, WindowMinimise() {},
        WindowMaximise() {}, WindowSetTitle() {}, Quit() {},
      };

      // ── window.go.main.App stubs ────────────────────────────────────────────
      const App = {
        GetAllSessions: () => rpc('GetAllSessions', {}),
        GetRateLimitStatus: () => rpc('GetRateLimitStatus', {}),
        GetDailyCost: (date: string) => rpc('GetDailyCost', { date }),
        GetConfig: () => rpc('GetConfig', {}),
        UpdateConfig: (cfg: unknown) => rpc('UpdateConfig', { config: cfg }),
        GetAutoModelRouting: () =>
          rpc<{ Optimization?: { AutoModelRouting?: boolean } }>('GetConfig', {}).then(
            (c) => (c as { Optimization?: { AutoModelRouting?: boolean } })?.Optimization?.AutoModelRouting ?? false,
          ),
        GetProjects: () =>
          rpc<{ Projects?: unknown[] }>('GetConfig', {}).then(
            (c) => (c as { Projects?: unknown[] })?.Projects ?? [],
          ),
        StartSession: (project: string, session: string) =>
          rpc('StartSession', { project, session }),
        StopSession: (id: string, soft: boolean) => rpc('StopSession', { id, soft }),
        StartProject: (project: string) => rpc('StartProject', { project }),
        StopProject: (project: string) => rpc('StopProject', { project }),
        StartSessionWithModel: (project: string, session: string, model: string, effort: string) =>
          rpc('StartSessionWithOverride', { project, session, model, effort }),
        RestartSession: (id: string) => rpc('RestartSession', { id }),
        ResumeSession: (id: string) => rpc('ResumeSession', { id }),
        StopAll: () => rpc('StopAll', {}),
        SendMessage: (id: string, message: string) => rpc('SendMessage', { id, message }),
        RespondPermission: (id: string, requestId: string, decision: string) =>
          rpc('RespondPermission', { id, request_id: requestId, decision }),
        GetPendingPermissions: () => rpc('GetPendingPermissions', {}),
        GetSessionLog: (id: string, offset: number, limit: number) =>
          rpc('GetSessionLog', { id, offset, limit }),
        GetHistory: (project: string, limit: number) => rpc('GetHistory', { project, limit }),
        GetSessionMetrics: (id: string) => rpc('GetSessionMetrics', { id }),
        GetProjectCost: (project: string, days: number) =>
          rpc('GetProjectCost', { project, days }),
        ClearSessionState: (project: string, name: string) =>
          rpc('ClearSessionState', { project, session: name }),
        GetSessionState: (project: string, name: string) =>
          rpc('GetSessionState', { project, session: name }),
        GetModelRecommendation: () => Promise.resolve(null),
        // Mixed programming (MIXED-TASKS.md MP-08).
        RegisterMixedBrief: (id: string, task: string, systemPrompt: string) =>
          rpc('RegisterMixedBrief', { id, task, system_prompt: systemPrompt }).then(() => id),
        DispatchMixedTask: (project: string, briefId: string, workerName: string) =>
          rpc('DispatchMixedTask', { project, brief_id: briefId, worker: workerName }),
        GetMixedRounds: (project: string) => rpc('GetMixedRounds', { project }),
        GetMixedQuality: (project: string) => rpc('GetMixedQuality', { project }),
        CancelMixedTask: (id: string) => rpc('CancelMixedTask', { id }),
        // GetLatestDraftRoadmap is called unconditionally for every project
        // with a Path as soon as Settings loads (Settings.svelte
        // loadAllRoadmapDrafts, called from load()) — leaving it undefined
        // throws synchronously ("... is not a function"), which load()'s own
        // catch turns into a persistent "Load failed" error that then hides
        // any later `info` message (the template shows error *or* info, error
        // wins), most visibly swallowing the "Saved." confirmation after
        // save()'s own re-`load()`. Not Wails-only in the sense of the other
        // roadmap calls below — this one runs on every Settings open/save
        // regardless of whether a spec ever touches the roadmap feature.
        GetLatestDraftRoadmap: () => Promise.resolve(null),
        // Stubs for platform-specific calls that don't exist in the test server.
        PickDirectory: () => Promise.resolve(''),
        Notify: () => Promise.resolve(undefined),
        MinimizeToTray: () => Promise.resolve(undefined),
        ShowMainWindow: () => Promise.resolve(undefined),
        ExportLog: () => Promise.resolve(''),
        CleanOldLogs: () => Promise.resolve(undefined),
        // GetTopActions/GetActionSamples (LEARN-TASKS.md LN-03) read straight
        // from the App's own SQLite store, which playwright-server never opens
        // (see cmd/playwright-server/main.go — SessionManager gets store=nil,
        // same reason History/CostDashboard have no real-data Playwright
        // coverage either). Stubbed empty so ExperiencePanel renders its
        // legitimate "no actions" empty state instead of an RPC error.
        GetTopActions: () => Promise.resolve([]),
        GetActionSamples: () => Promise.resolve([]),
        // GetPermissionCandidates (LEARN-TASKS.md LN-04) reads from the same
        // store as GetTopActions above — same reason, same stub shape.
        // AddPermissionRule *does* go through UpdateConfig in the real app,
        // but since GetPermissionCandidates never returns a row here, its
        // "Add rule" button is never reachable in this harness either.
        GetPermissionCandidates: () => Promise.resolve({ Safe: [], NeedsReview: [] }),
        AddPermissionRule: () => Promise.resolve(undefined),
        // GetDurationProfile (LEARN-TASKS.md LN-18) reads from the same store
        // as GetTopActions above — same reason, same stub shape.
        GetDurationProfile: () => Promise.resolve([]),
        // GetSkills/ApproveSkill/ArchiveSkill (LEARN-TASKS.md LN-10) read/write
        // the same store as GetTopActions above — same reason, same empty stub.
        // Tests that need a populated Skills tab override GetSkills per-test
        // (see skills.spec.ts), same pattern as the Permissions/Timing tabs.
        GetSkills: () => Promise.resolve([]),
        ApproveSkill: () => Promise.resolve(''),
        ArchiveSkill: () => Promise.resolve(undefined),
        // GetSkillQuality (LEARN-TASKS.md LN-11) reads from the same store as
        // GetSkills above — same reason, same empty stub.
        GetSkillQuality: () => Promise.resolve([]),
        // GetTokenAttribution (LEARN-TASKS.md LN-12) reads from the same store
        // as GetTopActions above — same reason, same empty stub shape.
        GetTokenAttribution: () => Promise.resolve({ TotalEstTokens: 0, BySignature: [], ByTool: [] }),
      };

      (window as typeof window & { go: unknown }).go = { main: { App } };

      // ── WebSocket event relay ────────────────────────────────────────────────
      const wsUrl = `ws://127.0.0.1:${p}/events?token=${encodeURIComponent(t)}`;
      const ws = new WebSocket(wsUrl);

      ws.onmessage = (ev) => {
        try {
          const env = JSON.parse(ev.data as string) as {
            event: string;
            data: string | Record<string, unknown>;
          };
          const data =
            typeof env.data === 'string' ? JSON.parse(env.data) : env.data;
          (window as typeof window & { __dispatchWailsEvent: (n: string, d: unknown) => void })
            .__dispatchWailsEvent(env.event, data);
        } catch { /* ignore malformed */ }
      };

      ws.onerror = () => {
        console.warn('[wails-bridge] WS error — events will not flow from backend');
      };
    },
    { p: port, t: token },
  );
}
