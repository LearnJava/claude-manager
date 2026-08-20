<script lang="ts">
    import { createEventDispatcher, onMount, onDestroy } from 'svelte';
    import {
        GetConfig,
        UpdateConfig,
        PickDirectory,
        GenerateRoadmap,
        GetLatestDraftRoadmap,
        GetProjectLogFiles,
        ClearProjectLogs,
    } from '../../wailsjs/go/main/App';
    import { EventsOn } from '../../wailsjs/runtime/runtime';
    import { initProjects } from '../stores/projects';
    import { refreshSessions } from '../stores/sessions';
    import { setTheme, type Theme } from '../stores/theme';
    import { formatBytes } from '../lib/formatters';
    import { MODELS, EFFORTS, isKnownModel } from '../lib/models';
    import PlanReview from './PlanReview.svelte';

    const dispatch = createEventDispatcher();

    type Tab = 'global' | 'projects' | 'sessions' | 'workers';
    export let initialTab: Tab = 'global';
    export let initialAction: 'add' | undefined = undefined;
    let activeTab: Tab = initialTab;

    const tabs: { id: Tab; label: string }[] = [
        { id: 'global', label: 'Global' },
        { id: 'projects', label: 'Projects' },
        { id: 'sessions', label: 'Sessions' },
        { id: 'workers', label: 'Workers' },
    ];

    // ---- Local config types (mirror Go structs from internal/config/types.go) ----

    interface PermissionRule {
        Tool: string;
        Pattern: string;
        Decision: string;
    }

    interface SessionConfig {
        Name: string;
        Prompt: string;
        AutoRestart: boolean;
        MaxTasks: number;
        StopWhenNoTasks: boolean;
        TaskSource: string;
        Model: string;
        FallbackModel: string;
        FallbackModelOnRateLimit: boolean;
        Effort: string;
        PermissionMode: string;
        AllowedTools: string[];
        DisallowedTools: string[];
        PermissionRules: PermissionRule[];
        MaxBudgetUSD: number;
        UseWorktree: boolean;
        SystemPromptAppend: string;
        AddDirs: string[];
        Preflight: string;
        PreTaskHook: string;
        PostTaskHook: string;
        CrashRecoveryPrompt: string;
    }

    interface ProjectConfig {
        Name: string;
        Path: string;
        Sessions: SessionConfig[];
        // Seeds PermissionMode for sessions newly added to this project; empty
        // means "use the built-in default" (bypassPermissions). Never touches
        // an existing session's saved value.
        DefaultPermissionMode: string;
        // Mixed programming (MIXED-TASKS.md). Privacy opt-in: code is sent to
        // external worker endpoints. Enabling requires non-empty Gates.
        MixedProgramming: boolean;
        Gates: string[];
        MixedMaxRounds: number;
    }

    interface WorkerConfig {
        Name: string;
        BaseURL: string;
        Model: string;
        KeyEnv: string;
        Role: string;
        ReasoningEffort: string;
        MaxOutputTokens: number;
        ContinuationCap: number;
        ASCIIAnchorsOnly: boolean;
        RequestTimeoutSec: number;
    }

    interface GlobalSettings {
        ClaudePath: string;
        DefaultRetryDelay: number;
        RateLimitPause: number;
        LogRetentionDays: number;
        Theme: string;
        CrashRecovery: boolean;
        PreflightAnalysis: boolean;
        PreflightModel: string;
        PreflightMaxBudget: number;
        PreflightAutoApproveSingle: boolean;
        PermissionNotifyAfter: number;
        PermissionTimeout: number;
        PermissionTimeoutAction: string;
        PermissionNativeNotification: boolean;
        PermissionSound: boolean;
        DailyBudgetAlert: number;
        WeeklyBudgetAlert: number;
        RateLimitAlertThreshold: number;
        SessionStartDelay?: number;
    }

    interface OptimizationSettings {
        AutoModelRouting: boolean;
    }

    interface AppConfig {
        Settings: GlobalSettings;
        Optimization: OptimizationSettings;
        Projects: ProjectConfig[];
        Workers: WorkerConfig[];
    }

    // ---- State ----

    let cfg: AppConfig | null = null;
    let loading = true;
    let saving = false;
    let error = '';
    let info = '';
    let selectedProjectIdx = 0;
    let selectedSessionIdx = 0;

    function emptySession(name = 'new-session', defaultPermissionMode = ''): SessionConfig {
        return {
            Name: name,
            Prompt: '',
            AutoRestart: false,
            MaxTasks: 0,
            StopWhenNoTasks: false,
            TaskSource: '',
            Model: 'sonnet',
            FallbackModel: '',
            FallbackModelOnRateLimit: false,
            Effort: 'high',
            PermissionMode: defaultPermissionMode || 'bypassPermissions',
            AllowedTools: [],
            DisallowedTools: [],
            PermissionRules: [],
            MaxBudgetUSD: 0,
            UseWorktree: false,
            SystemPromptAppend: '',
            AddDirs: [],
            Preflight: 'auto',
            PreTaskHook: '',
            PostTaskHook: '',
            CrashRecoveryPrompt: '',
        };
    }

    function emptyProject(name = 'new-project'): ProjectConfig {
        return {
            Name: name,
            Path: '',
            Sessions: [],
            DefaultPermissionMode: '',
            MixedProgramming: false,
            Gates: [],
            MixedMaxRounds: 3,
        };
    }

    function emptyWorker(name = 'new-worker'): WorkerConfig {
        return {
            Name: name,
            BaseURL: '',
            Model: '',
            KeyEnv: '',
            Role: 'hands',
            ReasoningEffort: 'low',
            MaxOutputTokens: 16000,
            ContinuationCap: 3,
            ASCIIAnchorsOnly: false,
            RequestTimeoutSec: 180,
        };
    }

    // Presets from the lumen bench (MIXED-TASKS.md "Рабочие модели").
    const KILO_URL = 'https://api.kilo.ai/api/gateway';
    const workerPresets: { label: string; make: () => WorkerConfig }[] = [
        {
            label: 'Step 3.7 Flash (hands)',
            make: () => ({
                ...emptyWorker('step37'),
                BaseURL: KILO_URL,
                Model: 'stepfun/step-3.7-flash:free',
                KeyEnv: 'KILO_API_KEY',
                Role: 'hands',
            }),
        },
        {
            label: 'Nemotron 3 Ultra (quality)',
            make: () => ({
                ...emptyWorker('nemotron-ultra'),
                BaseURL: KILO_URL,
                Model: 'nvidia/nemotron-3-ultra-550b-a55b:free',
                KeyEnv: 'KILO_API_KEY',
                Role: 'quality',
                ASCIIAnchorsOnly: true,
            }),
        },
    ];

    let selectedWorkerIdx = 0;

    function addWorker() {
        if (!cfg) return;
        cfg.Workers = [...cfg.Workers, emptyWorker(`worker-${cfg.Workers.length + 1}`)];
        selectedWorkerIdx = cfg.Workers.length - 1;
    }

    function addWorkerPreset(idx: number) {
        if (!cfg) return;
        cfg.Workers = [...cfg.Workers, workerPresets[idx].make()];
        selectedWorkerIdx = cfg.Workers.length - 1;
    }

    function removeWorker(idx: number) {
        if (!cfg) return;
        cfg.Workers = cfg.Workers.filter((_, i) => i !== idx);
        if (selectedWorkerIdx >= cfg.Workers.length) {
            selectedWorkerIdx = Math.max(0, cfg.Workers.length - 1);
        }
    }


    function normaliseConfig(raw: any): AppConfig {
        const settings: GlobalSettings = {
            ClaudePath: '',
            DefaultRetryDelay: 30,
            RateLimitPause: 300,
            LogRetentionDays: 30,
            Theme: 'dark',
            CrashRecovery: true,
            PreflightAnalysis: true,
            PreflightModel: 'haiku',
            PreflightMaxBudget: 0,
            PreflightAutoApproveSingle: true,
            PermissionNotifyAfter: 30,
            PermissionTimeout: 0,
            PermissionTimeoutAction: 'deny',
            PermissionNativeNotification: true,
            PermissionSound: true,
            DailyBudgetAlert: 0,
            WeeklyBudgetAlert: 0,
            RateLimitAlertThreshold: 0.8,
            SessionStartDelay: 3,
            ...(raw?.Settings ?? {}),
        };
        const projects: ProjectConfig[] = (raw?.Projects ?? []).map((p: any) => ({
            Name: p?.Name ?? '',
            Path: p?.Path ?? '',
            DefaultPermissionMode: p?.DefaultPermissionMode ?? '',
            MixedProgramming: p?.MixedProgramming ?? false,
            Gates: p?.Gates ?? [],
            MixedMaxRounds: p?.MixedMaxRounds ?? 3,
            Sessions: (p?.Sessions ?? []).map((s: any) => ({
                ...emptySession(s?.Name ?? '', p?.DefaultPermissionMode ?? ''),
                ...s,
                AllowedTools: s?.AllowedTools ?? [],
                DisallowedTools: s?.DisallowedTools ?? [],
                AddDirs: s?.AddDirs ?? [],
                PermissionRules: (s?.PermissionRules ?? []).map((r: any) => ({
                    Tool: r?.Tool ?? '',
                    Pattern: r?.Pattern ?? '',
                    Decision: r?.Decision ?? 'allow',
                })),
            })),
        }));
        const workers: WorkerConfig[] = (raw?.Workers ?? []).map((w: any) => ({
            ...emptyWorker(w?.Name ?? ''),
            ...w,
        }));
        const optimization: OptimizationSettings = {
            AutoModelRouting: false,
            ...(raw?.Optimization ?? {}),
        };
        return { Settings: settings, Optimization: optimization, Projects: projects, Workers: workers };
    }

    async function load() {
        loading = true;
        error = '';
        try {
            const raw = await GetConfig();
            cfg = normaliseConfig(raw);
            // Apply persisted theme from config to the live theme store so the
            // UI matches the saved value when the dialog opens.
            const t = cfg.Settings.Theme === 'light' ? 'light' : 'dark';
            setTheme(t as Theme);
            clampSelections();
            if (initialAction === 'add') {
                addProject();
            }
            loadAllProjectLogInfo();
            loadAllRoadmapDrafts();
        } catch (e: any) {
            error = `Load failed: ${e?.message ?? String(e)}`;
        } finally {
            loading = false;
        }
    }

    onMount(load);

    // Live progress for an in-flight GenerateRoadmap call (see CLAUDE.md
    // "AI-Generated Project Roadmap… Progress reporting & recovery") — without
    // this, a multi-minute Opus run looks identical to a hung one.
    let unsubRoadmapProgress: (() => void) | null = null;
    onMount(() => {
        unsubRoadmapProgress = EventsOn('plan:roadmap_progress', (evt: { project: string; text: string }) => {
            if (!cfg) return;
            const idx = cfg.Projects.findIndex((p) => p.Name === evt.project);
            if (idx === -1) return;
            roadmapProgress[idx] = evt.text;
            roadmapProgress = roadmapProgress;
        });
    });
    onDestroy(() => {
        unsubRoadmapProgress?.();
        if (roadmapElapsedTimer) clearInterval(roadmapElapsedTimer);
    });

    function clampSelections() {
        if (!cfg) return;
        if (cfg.Projects.length === 0) {
            selectedProjectIdx = 0;
            selectedSessionIdx = 0;
            return;
        }
        if (selectedProjectIdx >= cfg.Projects.length) {
            selectedProjectIdx = cfg.Projects.length - 1;
        }
        const sessions = cfg.Projects[selectedProjectIdx]?.Sessions ?? [];
        if (sessions.length === 0) {
            selectedSessionIdx = 0;
        } else if (selectedSessionIdx >= sessions.length) {
            selectedSessionIdx = sessions.length - 1;
        }
    }

    // ---- Tools / arrays helpers ----

    function joinList(list: string[]): string {
        return (list ?? []).join('\n');
    }
    function splitList(value: string): string[] {
        return value
            .split('\n')
            .map((s) => s.trim())
            .filter((s) => s.length > 0);
    }

    // Helpers for the textarea↔string[] bindings. Svelte's template parser
    // chokes on inline `as HTMLTextAreaElement` casts, so we use named handlers.
    function onAllowedToolsInput(e: Event) {
        if (sess) sess.AllowedTools = splitList((e.currentTarget as HTMLTextAreaElement).value);
    }
    function onDisallowedToolsInput(e: Event) {
        if (sess) sess.DisallowedTools = splitList((e.currentTarget as HTMLTextAreaElement).value);
    }
    function onAddDirsInput(e: Event) {
        if (sess) sess.AddDirs = splitList((e.currentTarget as HTMLTextAreaElement).value);
    }
    function onProjectGatesInput(e: Event, idx: number) {
        if (cfg) cfg.Projects[idx].Gates = splitList((e.currentTarget as HTMLTextAreaElement).value);
    }

    function onThemeChange(e: Event) {
        const v = (e.currentTarget as HTMLSelectElement).value as Theme;
        if (gs) gs.Theme = v;
        setTheme(v);
    }

    // ---- Projects tab actions ----

    function addProject() {
        if (!cfg) return;
        cfg.Projects = [...cfg.Projects, emptyProject(`project-${cfg.Projects.length + 1}`)];
        selectedProjectIdx = cfg.Projects.length - 1;
        selectedSessionIdx = 0;
    }

    function removeProject(idx: number) {
        if (!cfg) return;
        cfg.Projects = cfg.Projects.filter((_, i) => i !== idx);
        clampSelections();
    }

    async function pickProjectPath(idx: number) {
        try {
            const chosen = await PickDirectory('Select project folder');
            if (chosen && cfg) {
                cfg.Projects[idx].Path = chosen;
                cfg = cfg; // trigger reactivity
            }
        } catch (e: any) {
            error = `Folder picker failed: ${e?.message ?? String(e)}`;
        }
    }

    // ---- AI roadmap generation ----
    // Ephemeral, per-project-index state — not part of AppConfig, so it isn't
    // saved/loaded and resets when Settings is reopened.
    let roadmapIdea: Record<number, string> = {};
    let roadmapModel: Record<number, string> = {};
    let roadmapBusy: number | null = null;
    let roadmapPlan: any = null;

    // Live progress + elapsed timer while a generation is in flight (see
    // CLAUDE.md "Progress reporting & recovery") — GenerateRoadmap on Opus can
    // run 5-10+ minutes, and without this the button gives no sign of life.
    let roadmapProgress: Record<number, string> = {};
    let roadmapStartedAt: Record<number, number> = {};
    let roadmapElapsedTick = 0;
    let roadmapElapsedTimer: ReturnType<typeof setInterval> | null = null;
    $: roadmapElapsedText = (() => {
        roadmapElapsedTick; // reactive dependency — recompute every tick
        if (roadmapBusy === null || !roadmapStartedAt[roadmapBusy]) return '';
        const secs = Math.floor((Date.now() - roadmapStartedAt[roadmapBusy]) / 1000);
        const m = Math.floor(secs / 60);
        const s = secs % 60;
        return `${m}:${s.toString().padStart(2, '0')}`;
    })();

    // Draft roadmap plans recovered from a previous GenerateRoadmap call whose
    // result never reached this page (dropped IPC response, reload mid-request)
    // — see GetLatestDraftRoadmap. Dismissal is client-side only, same as the
    // "No CLAUDE.md" banner in Sidebar.svelte.
    let roadmapDraft: Record<number, any> = {};
    let roadmapDraftDismissed: Set<number> = new Set();

    function loadAllRoadmapDrafts() {
        if (!cfg) return;
        cfg.Projects.forEach((p, i) => {
            if (!p.Path) return;
            GetLatestDraftRoadmap(p.Name)
                .then((plan) => {
                    if (plan) {
                        roadmapDraft[i] = plan;
                        roadmapDraft = roadmapDraft;
                    }
                })
                .catch(() => {});
        });
    }

    function openRecoveredDraft(idx: number) {
        roadmapPlan = roadmapDraft[idx];
    }

    function dismissRoadmapDraft(idx: number) {
        roadmapDraftDismissed.add(idx);
        roadmapDraftDismissed = roadmapDraftDismissed;
    }

    async function generateRoadmapForProject(idx: number) {
        if (!cfg) return;
        const idea = (roadmapIdea[idx] ?? '').trim();
        if (!idea) return;
        roadmapBusy = idx;
        roadmapProgress[idx] = '';
        roadmapStartedAt[idx] = Date.now();
        if (!roadmapElapsedTimer) {
            roadmapElapsedTimer = setInterval(() => { roadmapElapsedTick++; }, 1000);
        }
        error = '';
        info = '';
        try {
            roadmapPlan = await GenerateRoadmap(cfg.Projects[idx].Name, idea, roadmapModel[idx] ?? 'opus');
            delete roadmapDraft[idx];
            roadmapDraft = roadmapDraft;
        } catch (e: any) {
            error = `Roadmap generation failed: ${e?.message ?? String(e)}`;
            // The plan may still have been generated and saved server-side even
            // though this call itself failed/never resolved cleanly — re-check
            // so a paid-for run isn't silently stranded.
            loadAllRoadmapDrafts();
        } finally {
            roadmapBusy = null;
            if (roadmapElapsedTimer) {
                clearInterval(roadmapElapsedTimer);
                roadmapElapsedTimer = null;
            }
        }
    }

    function onRoadmapCancel() {
        roadmapPlan = null;
    }

    async function onRoadmapWritten() {
        roadmapPlan = null;
        info = 'ROADMAP.md and STATUS-P1.md written — the "P1" session is configured.';
        await load();
    }

    // ---- Project log files (auto-saved on task/run completion — see
    // CLAUDE.md "Automatic Log Saving") ----
    let projectLogInfo: Record<number, { count: number; size: number } | null> = {};
    let logsBusy: number | null = null;
    let logsPendingClear: number | null = null;
    let logsPendingClearTimer: ReturnType<typeof setTimeout> | null = null;

    async function loadProjectLogInfo(idx: number) {
        if (!cfg) return;
        const p = cfg.Projects[idx];
        if (!p?.Path) {
            projectLogInfo[idx] = null;
            return;
        }
        try {
            const files = await GetProjectLogFiles(p.Name);
            const size = (files ?? []).reduce((sum, f) => sum + (f.size ?? 0), 0);
            projectLogInfo[idx] = { count: (files ?? []).length, size };
        } catch (e: any) {
            projectLogInfo[idx] = null;
        }
        projectLogInfo = projectLogInfo;
    }

    function loadAllProjectLogInfo() {
        if (!cfg) return;
        cfg.Projects.forEach((p, i) => {
            if (p.Path) loadProjectLogInfo(i);
        });
    }

    function requestClearProjectLogs(idx: number) {
        if (logsPendingClear === idx) {
            // Second click — confirmed.
            if (logsPendingClearTimer) clearTimeout(logsPendingClearTimer);
            logsPendingClearTimer = null;
            logsPendingClear = null;
            clearProjectLogs(idx);
            return;
        }
        // First click — arm the button, auto-cancel after 3 seconds.
        logsPendingClear = idx;
        if (logsPendingClearTimer) clearTimeout(logsPendingClearTimer);
        logsPendingClearTimer = setTimeout(() => {
            logsPendingClear = null;
            logsPendingClearTimer = null;
        }, 3000);
    }

    async function clearProjectLogs(idx: number) {
        if (!cfg) return;
        const p = cfg.Projects[idx];
        logsBusy = idx;
        error = '';
        try {
            await ClearProjectLogs(p.Name);
            await loadProjectLogInfo(idx);
            info = `Cleared saved logs for ${p.Name}.`;
        } catch (e: any) {
            error = `Clear logs failed: ${e?.message ?? String(e)}`;
        } finally {
            logsBusy = null;
        }
    }

    // ---- Sessions tab actions ----

    function addSession() {
        if (!cfg || cfg.Projects.length === 0) return;
        const proj = cfg.Projects[selectedProjectIdx];
        proj.Sessions = [
            ...proj.Sessions,
            emptySession(`S${proj.Sessions.length + 1}`, proj.DefaultPermissionMode),
        ];
        selectedSessionIdx = proj.Sessions.length - 1;
        cfg = cfg;
    }

    function removeSession(idx: number) {
        if (!cfg) return;
        const proj = cfg.Projects[selectedProjectIdx];
        if (!proj) return;
        proj.Sessions = proj.Sessions.filter((_, i) => i !== idx);
        clampSelections();
        cfg = cfg;
    }

    function addPermissionRule() {
        const s = currentSession();
        if (!s) return;
        s.PermissionRules = [
            ...s.PermissionRules,
            { Tool: 'Bash', Pattern: '*', Decision: 'allow' },
        ];
        cfg = cfg;
    }

    function removePermissionRule(idx: number) {
        const s = currentSession();
        if (!s) return;
        s.PermissionRules = s.PermissionRules.filter((_, i) => i !== idx);
        cfg = cfg;
    }

    function currentSession(): SessionConfig | null {
        if (!cfg) return null;
        const proj = cfg.Projects[selectedProjectIdx];
        if (!proj) return null;
        return proj.Sessions[selectedSessionIdx] ?? null;
    }

    // ---- Save ----

    async function save() {
        if (!cfg || saving) return;
        saving = true;
        error = '';
        info = '';
        try {
            // Re-build with normalised list fields so we don't send strings where Go expects []string.
            const payload: AppConfig = JSON.parse(JSON.stringify(cfg));
            await UpdateConfig(payload as any);
            await initProjects();
            await refreshSessions();
            info = 'Saved.';
            // Re-pull so we see canonicalised values.
            await load();
        } catch (e: any) {
            error = `Save failed: ${e?.message ?? String(e)}`;
        } finally {
            saving = false;
        }
    }

    function close() {
        dispatch('close');
    }

    function handleKey(e: KeyboardEvent) {
        if (e.key === 'Escape') close();
    }

    $: gs = cfg?.Settings;
    $: proj = cfg?.Projects[selectedProjectIdx] ?? null;
    $: sess = proj?.Sessions[selectedSessionIdx] ?? null;
    $: wrk = cfg?.Workers[selectedWorkerIdx] ?? null;
</script>

<svelte:window on:keydown={handleKey} />

<div
    class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
    on:click={close}
    on:keydown={(e) => e.key === 'Escape' && close()}
    role="dialog"
    aria-modal="true"
    tabindex="-1">
    <div
        class="bg-bg-panel border border-bg-border rounded-md shadow-xl w-[1640px] max-w-[95vw] h-[1280px] max-h-[92vh] flex flex-col"
        role="document"
        on:click|stopPropagation
        on:keydown|stopPropagation>
        <!-- Header -->
        <div class="px-4 py-3 border-b border-bg-border flex items-center justify-between shrink-0">
            <h2 class="text-text font-semibold text-base">Settings</h2>
            <button
                class="text-text-muted hover:text-text text-sm px-2 py-0.5"
                on:click={close}
                type="button">✕</button>
        </div>

        <!-- Tabs -->
        <div class="px-4 pt-3 border-b border-bg-border flex gap-1 shrink-0">
            {#each tabs as t}
                <button
                    type="button"
                    class="px-3 py-1.5 text-sm rounded-t border-b-2 transition-colors
                        {activeTab === t.id
                            ? 'text-text border-blue-500 bg-bg-elevated'
                            : 'text-text-muted border-transparent hover:text-text hover:bg-bg-elevated/50'}"
                    on:click={() => (activeTab = t.id)}>{t.label}</button>
            {/each}
        </div>

        <!-- Body -->
        <div class="flex-1 min-h-0 overflow-y-auto px-4 py-4">
            {#if loading}
                <div class="text-text-muted text-sm italic py-10 text-center">Loading config…</div>
            {:else if !cfg || !gs}
                <div class="text-status-error text-sm py-10 text-center">No config loaded.</div>
            {:else if activeTab === 'global'}
                <!-- ───────── GLOBAL TAB ───────── -->
                <div class="space-y-6">
                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">General</h3>
                        <div class="grid grid-cols-2 gap-3">
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Claude CLI path
                                <input
                                    type="text"
                                    bind:value={gs.ClaudePath}
                                    placeholder="claude"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Theme
                                <select
                                    value={gs.Theme}
                                    on:change={onThemeChange}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                    <option value="dark">dark</option>
                                    <option value="light">light</option>
                                </select>
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Retry delay (sec)
                                <input
                                    type="number"
                                    bind:value={gs.DefaultRetryDelay}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Rate limit pause (sec)
                                <input
                                    type="number"
                                    bind:value={gs.RateLimitPause}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Log retention (days)
                                <input
                                    type="number"
                                    bind:value={gs.LogRetentionDays}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Session start delay (sec)
                                <input
                                    type="number"
                                    bind:value={gs.SessionStartDelay}
                                    min="0"
                                    title="Cache warming: stagger session starts by this many seconds."
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                        </div>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">Crash recovery</h3>
                        <div class="space-y-2">
                            <label class="flex items-center gap-2 text-sm text-text">
                                <input type="checkbox" bind:checked={gs.CrashRecovery} />
                                Enable crash recovery
                            </label>
                            <p class="text-xs text-text-muted leading-relaxed">
                                When enabled, the manager saves a session state file to
                                <code class="bg-bg px-1 rounded">~/.claude-manager/state/</code>
                                before each task. If the app is closed or crashes while a session
                                is running, the next start will resume the interrupted conversation
                                via <code class="bg-bg px-1 rounded">--resume</code>.
                                Use <b>Force new</b> per session (in Sessions tab) to discard saved
                                state and start fresh.
                            </p>
                        </div>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">Pre-flight analysis</h3>
                        <div class="grid grid-cols-2 gap-3">
                            <label class="flex items-center gap-2 text-sm text-text">
                                <input type="checkbox" bind:checked={gs.PreflightAnalysis} />
                                Enable pre-flight analysis
                            </label>
                            <label class="flex items-center gap-2 text-sm text-text">
                                <input type="checkbox" bind:checked={gs.PreflightAutoApproveSingle} />
                                Auto-approve single-session plans
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Analyst model
                                <input
                                    type="text"
                                    bind:value={gs.PreflightModel}
                                    placeholder="haiku"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Analyst max budget USD (0 = none)
                                <input
                                    type="number"
                                    step="0.01"
                                    min="0"
                                    bind:value={gs.PreflightMaxBudget}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                        </div>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">Permission timeouts</h3>
                        <div class="grid grid-cols-2 gap-3">
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Notify after (sec)
                                <input
                                    type="number"
                                    bind:value={gs.PermissionNotifyAfter}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Timeout (sec, 0 = wait forever)
                                <input
                                    type="number"
                                    bind:value={gs.PermissionTimeout}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Timeout action
                                <select
                                    bind:value={gs.PermissionTimeoutAction}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                    <option value="deny">deny</option>
                                    <option value="pause_session">pause_session</option>
                                </select>
                            </label>
                            <div class="flex flex-col gap-2 mt-4">
                                <label class="flex items-center gap-2 text-sm text-text">
                                    <input
                                        type="checkbox"
                                        bind:checked={gs.PermissionNativeNotification} />
                                    Native notification (Windows toast)
                                </label>
                                <label class="flex items-center gap-2 text-sm text-text">
                                    <input type="checkbox" bind:checked={gs.PermissionSound} />
                                    Sound alert
                                </label>
                            </div>
                        </div>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">Model routing</h3>
                        <label class="flex items-center gap-2 text-sm text-text">
                            <input type="checkbox" bind:checked={cfg.Optimization.AutoModelRouting} />
                            Auto model routing (choose model by task complexity via pre-flight)
                        </label>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">Budget alerts</h3>
                        <div class="grid grid-cols-3 gap-3">
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Daily alert (USD, 0 = off)
                                <input
                                    type="number"
                                    step="0.01"
                                    min="0"
                                    bind:value={gs.DailyBudgetAlert}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Weekly alert (USD, 0 = off)
                                <input
                                    type="number"
                                    step="0.01"
                                    min="0"
                                    bind:value={gs.WeeklyBudgetAlert}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Rate-limit alert threshold
                                <input
                                    type="number"
                                    step="0.05"
                                    min="0"
                                    max="1"
                                    bind:value={gs.RateLimitAlertThreshold}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                        </div>
                    </section>
                </div>
            {:else if activeTab === 'projects'}
                <!-- ───────── PROJECTS TAB ───────── -->
                <div class="space-y-3">
                    <div class="flex items-center justify-between">
                        <span class="text-text-muted text-xs">
                            {cfg.Projects.length} project{cfg.Projects.length === 1 ? '' : 's'} configured
                        </span>
                        <button
                            type="button"
                            on:click={addProject}
                            class="px-2 py-1 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white">
                            + Add project
                        </button>
                    </div>

                    {#if cfg.Projects.length === 0}
                        <div class="text-text-muted text-sm italic py-6 text-center">
                            No projects yet. Click <b>Add project</b> to create one.
                        </div>
                    {:else}
                        <ul class="space-y-3">
                            {#each cfg.Projects as p, i (i)}
                                <li class="bg-bg-elevated border border-bg-border rounded p-3 space-y-2">
                                    <div class="flex items-center justify-between">
                                        <span class="text-text-muted text-xs">Project #{i + 1}</span>
                                        <button
                                            type="button"
                                            on:click={() => removeProject(i)}
                                            class="px-2 py-0.5 text-xs rounded
                                                   bg-status-error/80 hover:bg-status-error text-white">
                                            Remove
                                        </button>
                                    </div>

                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Name
                                        <input
                                            type="text"
                                            bind:value={p.Name}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                    </label>

                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Path
                                        <div class="flex gap-2">
                                            <input
                                                type="text"
                                                bind:value={p.Path}
                                                placeholder="D:\Projects\my-app"
                                                class="flex-1 bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                            <button
                                                type="button"
                                                on:click={() => pickProjectPath(i)}
                                                class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg">
                                                Browse…
                                            </button>
                                        </div>
                                        {#if p.Path}
                                            <span class="text-[11px] text-text-muted/80 leading-snug" data-testid={`storage-note-${i}`}>
                                                📁 Sessions &amp; gates are saved in
                                                <code class="font-mono">{p.Path}\.claude-manager\config.toml</code>
                                                (commit it to share project context). The mixed-programming
                                                opt-in goes to <code class="font-mono">config.local.toml</code>
                                                (gitignored, never committed).
                                            </span>
                                        {:else}
                                            <span class="text-[11px] text-text-muted/60 leading-snug">
                                                Without a project folder, settings stay in the global config.
                                            </span>
                                        {/if}
                                    </label>

                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Default permission mode for new sessions
                                        <select
                                            bind:value={p.DefaultPermissionMode}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                            <option value="">(inherit → bypassPermissions)</option>
                                            <option value="bypassPermissions">bypassPermissions</option>
                                            <option value="acceptEdits">acceptEdits</option>
                                            <option value="default">default</option>
                                            <option value="auto">auto</option>
                                            <option value="plan">plan</option>
                                            <option value="dontAsk">dontAsk</option>
                                        </select>
                                        <span class="text-[11px] text-text-muted/70 leading-snug">
                                            Only seeds new sessions added to this project from now on —
                                            existing sessions keep their own saved value (editable in the
                                            Sessions tab).
                                        </span>
                                    </label>

                                    <div class="flex items-center justify-between">
                                        <span class="text-xs text-text-muted">
                                            {p.Sessions.length} session{p.Sessions.length === 1 ? '' : 's'}
                                        </span>
                                        <button
                                            type="button"
                                            on:click={() => { selectedProjectIdx = i; activeTab = 'sessions'; }}
                                            class="px-2 py-0.5 text-xs rounded bg-bg border border-bg-border text-text-muted hover:text-text hover:border-text-muted">
                                            + Add session →
                                        </button>
                                    </div>

                                    <!-- Mixed programming (MIXED-TASKS.md) -->
                                    <div class="border-t border-bg-border pt-2 mt-1 space-y-2">
                                        <label class="flex items-center gap-2 text-sm text-text">
                                            <input
                                                type="checkbox"
                                                data-testid={`mixed-enable-${i}`}
                                                bind:checked={p.MixedProgramming} />
                                            Enable mixed programming (external workers)
                                        </label>
                                        {#if p.MixedProgramming}
                                            <p class="text-xs text-status-error/90 leading-relaxed">
                                                ⚠ Briefs and verbatim code excerpts are sent to external
                                                free endpoints that log requests. Only enable for projects
                                                whose code may leave your machine.
                                                <span class="text-text-muted">This opt-in is stored in
                                                <code class="font-mono">config.local.toml</code> and is not
                                                committed — each teammate opts in for themselves.</span>
                                            </p>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Gates (one command per line — blocking; a non-zero exit
                                                rejects the round)
                                                <textarea
                                                    rows="2"
                                                    value={joinList(p.Gates)}
                                                    on:input={(e) => onProjectGatesInput(e, i)}
                                                    placeholder="go build ./...&#10;go test ./..."
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                                ></textarea>
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1 w-40">
                                                Max feedback rounds
                                                <input
                                                    type="number"
                                                    min="1"
                                                    bind:value={p.MixedMaxRounds}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                        {/if}
                                    </div>

                                    <!-- AI roadmap generation -->
                                    <div class="border-t border-bg-border pt-2 mt-1 space-y-2">
                                        <div class="text-xs text-text-muted">
                                            🤖 Describe the project and let AI draft a roadmap: it decomposes
                                            the idea into a backlog of session-sized tasks, writes
                                            <code class="font-mono">ROADMAP.md</code> +
                                            <code class="font-mono">STATUS-P1.md</code> into the project, and
                                            configures a "P1" Sonnet session to work through them one at a time.
                                        </div>
                                        {#if roadmapDraft[i] && !roadmapDraftDismissed.has(i) && roadmapBusy !== i}
                                            <div class="flex items-center justify-between gap-2 text-xs bg-amber-500/10
                                                        border border-amber-500/40 rounded px-2 py-1.5">
                                                <span class="text-text">
                                                    Found an unreviewed roadmap from a previous run —
                                                    {roadmapDraft[i].subtasks?.length ?? 0} tasks,
                                                    already generated (nothing spent re-running it).
                                                </span>
                                                <div class="flex items-center gap-1 shrink-0">
                                                    <button
                                                        type="button"
                                                        on:click={() => openRecoveredDraft(i)}
                                                        class="px-2 py-0.5 rounded bg-amber-600 hover:bg-amber-500 text-white">
                                                        Review
                                                    </button>
                                                    <button
                                                        type="button"
                                                        on:click={() => dismissRoadmapDraft(i)}
                                                        class="px-2 py-0.5 rounded bg-bg border border-bg-border
                                                               text-text-muted hover:text-text">
                                                        Dismiss
                                                    </button>
                                                </div>
                                            </div>
                                        {/if}
                                        <label class="flex flex-col text-xs text-text-muted gap-1">
                                            Project idea
                                            <textarea
                                                rows="3"
                                                bind:value={roadmapIdea[i]}
                                                disabled={!p.Path}
                                                placeholder="What do you want to build?"
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text resize-y disabled:opacity-50"
                                            ></textarea>
                                        </label>
                                        <div class="flex items-center gap-2">
                                            <select
                                                value={roadmapModel[i] ?? 'opus'}
                                                on:change={(e) => (roadmapModel[i] = e.currentTarget.value)}
                                                disabled={!p.Path}
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-xs text-text disabled:opacity-50">
                                                {#each MODELS as m}
                                                    <option value={m.value}>
                                                        {m.label}{m.value === 'opus' ? ' (recommended)' : ''}
                                                    </option>
                                                {/each}
                                            </select>
                                            <button
                                                type="button"
                                                on:click={() => generateRoadmapForProject(i)}
                                                disabled={!p.Path || !(roadmapIdea[i] ?? '').trim() || roadmapBusy !== null}
                                                class="px-2 py-1 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white
                                                       disabled:opacity-50 disabled:cursor-not-allowed">
                                                {roadmapBusy === i ? `Generating… ${roadmapElapsedText}` : 'Generate Roadmap with AI'}
                                            </button>
                                        </div>
                                        {#if roadmapBusy === i}
                                            <div class="text-[11px] text-text-muted font-mono truncate" title={roadmapProgress[i] ?? ''}>
                                                {roadmapProgress[i] ? roadmapProgress[i] : 'Starting analyst session…'}
                                            </div>
                                        {/if}
                                        {#if !p.Path}
                                            <span class="text-[11px] text-text-muted/60">
                                                Set a project folder above first.
                                            </span>
                                        {/if}
                                    </div>

                                    <!-- Saved session logs (auto-saved on task/run completion into
                                         <project>/.claude-manager/logs/) -->
                                    <div class="border-t border-bg-border pt-2 mt-1 space-y-2">
                                        <div class="flex items-center justify-between gap-2">
                                            <span class="text-xs text-text-muted">
                                                {#if !p.Path}
                                                    Saved logs: set a project folder above first.
                                                {:else if projectLogInfo[i]}
                                                    Saved logs: {projectLogInfo[i]?.count ?? 0} file{(projectLogInfo[i]?.count ?? 0) === 1 ? '' : 's'},
                                                    {formatBytes(projectLogInfo[i]?.size)}
                                                {:else}
                                                    Saved logs: —
                                                {/if}
                                            </span>
                                            <div class="flex items-center gap-1">
                                                <button
                                                    type="button"
                                                    on:click={() => loadProjectLogInfo(i)}
                                                    disabled={!p.Path}
                                                    title="Refresh saved-log count/size"
                                                    class="px-1.5 py-0.5 text-xs rounded bg-bg border border-bg-border
                                                           text-text-muted hover:text-text disabled:opacity-40 disabled:cursor-not-allowed">
                                                    🔄
                                                </button>
                                                <button
                                                    type="button"
                                                    on:click={() => requestClearProjectLogs(i)}
                                                    disabled={!p.Path || !projectLogInfo[i]?.count || logsBusy !== null}
                                                    title={logsPendingClear === i
                                                        ? 'Click again to confirm deletion'
                                                        : 'Delete every saved log file for this project'}
                                                    class="px-2 py-0.5 text-xs rounded border disabled:opacity-40 disabled:cursor-not-allowed
                                                           {logsPendingClear === i
                                                               ? 'bg-status-error/20 border-status-error/50 text-status-error font-semibold'
                                                               : 'bg-bg border-bg-border text-text-muted hover:text-status-error'}">
                                                    {#if logsBusy === i}…{:else if logsPendingClear === i}Confirm clear?{:else}🗑 Clear project logs{/if}
                                                </button>
                                            </div>
                                        </div>
                                        <p class="text-[11px] text-text-muted/70 leading-snug">
                                            Every finished task/run auto-saves its log as markdown here.
                                            Clearing removes those files and their SQLite log entries —
                                            History/Dashboard run records are kept.
                                        </p>
                                    </div>
                                </li>
                            {/each}
                        </ul>
                    {/if}
                </div>
            {:else if activeTab === 'sessions'}
                <!-- ───────── SESSIONS TAB ───────── -->
                {#if cfg.Projects.length === 0}
                    <div class="text-text-muted text-sm italic py-6 text-center">
                        Add a project first (Projects tab).
                    </div>
                {:else}
                    <div class="grid grid-cols-[200px_1fr] gap-4 h-full">
                        <!-- Left rail: project + session pickers -->
                        <div class="flex flex-col gap-3 min-h-0">
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                Project
                                <select
                                    bind:value={selectedProjectIdx}
                                    on:change={() => (selectedSessionIdx = 0)}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                    {#each cfg.Projects as p, i}
                                        <option value={i}>{p.Name || `(project ${i + 1})`}</option>
                                    {/each}
                                </select>
                            </label>

                            <div class="flex items-center justify-between">
                                <span class="text-text-muted text-xs">Sessions</span>
                                <button
                                    type="button"
                                    on:click={addSession}
                                    class="px-2 py-0.5 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white">
                                    + Add
                                </button>
                            </div>

                            <ul class="border border-bg-border rounded divide-y divide-bg-border bg-bg-elevated overflow-y-auto">
                                {#if proj && proj.Sessions.length === 0}
                                    <li class="px-2 py-2 text-xs text-text-muted italic">No sessions.</li>
                                {/if}
                                {#each proj?.Sessions ?? [] as s, i (i)}
                                    <li class="flex items-center">
                                        <button
                                            type="button"
                                            on:click={() => (selectedSessionIdx = i)}
                                            class="flex-1 text-left px-2 py-1 text-sm truncate
                                                {selectedSessionIdx === i
                                                    ? 'bg-bg text-text'
                                                    : 'text-text-muted hover:text-text hover:bg-bg/50'}">
                                            {s.Name || `(session ${i + 1})`}
                                        </button>
                                        <button
                                            type="button"
                                            on:click={() => removeSession(i)}
                                            title="Remove"
                                            class="px-1.5 py-1 text-xs text-text-muted hover:text-status-error">✕</button>
                                    </li>
                                {/each}
                            </ul>
                        </div>

                        <!-- Right pane: session editor -->
                        <div class="min-h-0 overflow-y-auto pr-1">
                            {#if !sess}
                                <div class="text-text-muted text-sm italic py-6 text-center">
                                    Select or add a session.
                                </div>
                            {:else}
                                <div class="space-y-5">
                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">Identity</h3>
                                        <div class="grid grid-cols-2 gap-3">
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Name
                                                <input
                                                    type="text"
                                                    bind:value={sess.Name}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Task source file
                                                <input
                                                    type="text"
                                                    bind:value={sess.TaskSource}
                                                    placeholder="STATUS-P1.md"
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                        </div>
                                        <label class="flex flex-col text-xs text-text-muted gap-1 mt-3">
                                            Prompt
                                            <textarea
                                                bind:value={sess.Prompt}
                                                rows="6"
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                            ></textarea>
                                        </label>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">Model</h3>
                                        <div class="grid grid-cols-3 gap-3">
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Model
                                                <select
                                                    bind:value={sess.Model}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                                    {#each MODELS as m}
                                                        <option value={m.value}>{m.label}</option>
                                                    {/each}
                                                    {#if sess.Model && !isKnownModel(sess.Model)}
                                                        <!-- A pinned id someone put in config.toml by hand -->
                                                        <option value={sess.Model}>{sess.Model}</option>
                                                    {/if}
                                                </select>
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Effort
                                                <select
                                                    bind:value={sess.Effort}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                                    {#each EFFORTS as e}
                                                        <option value={e}>{e}</option>
                                                    {/each}
                                                </select>
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Fallback model
                                                <input
                                                    type="text"
                                                    bind:value={sess.FallbackModel}
                                                    placeholder="haiku"
                                                    title="Model used when the primary model is rate-limited or overloaded."
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                        </div>
                                        <label class="flex items-center gap-2 text-sm text-text mt-3"
                                               title="When rate-limited: switch to fallback_model immediately and restart without waiting. Mirrors orchestrator.py fallback behaviour.">
                                            <input type="checkbox" bind:checked={sess.FallbackModelOnRateLimit} />
                                            Switch to fallback model on rate limit (no wait)
                                        </label>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">Permissions</h3>
                                        <label class="flex flex-col text-xs text-text-muted gap-1 mb-3">
                                            Permission mode
                                            <select
                                                bind:value={sess.PermissionMode}
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                                <option value="default">default</option>
                                                <option value="acceptEdits">acceptEdits</option>
                                                <option value="auto">auto</option>
                                                <option value="plan">plan</option>
                                                <option value="dontAsk">dontAsk</option>
                                                <option value="bypassPermissions">bypassPermissions</option>
                                            </select>
                                        </label>

                                        <div class="grid grid-cols-2 gap-3">
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Allowed tools (one per line)
                                                <textarea
                                                    rows="3"
                                                    value={joinList(sess.AllowedTools)}
                                                    on:input={onAllowedToolsInput}
                                                    placeholder="Bash(cargo:*)&#10;Bash(npm:*)"
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                                ></textarea>
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Disallowed tools (one per line)
                                                <textarea
                                                    rows="3"
                                                    value={joinList(sess.DisallowedTools)}
                                                    on:input={onDisallowedToolsInput}
                                                    placeholder="Bash(rm:*)"
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                                ></textarea>
                                            </label>
                                        </div>

                                        <div class="mt-4">
                                            <div class="flex items-center justify-between mb-1">
                                                <span class="text-text-muted text-xs">Auto-approve rules</span>
                                                <button
                                                    type="button"
                                                    on:click={addPermissionRule}
                                                    class="px-2 py-0.5 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white">
                                                    + Add rule
                                                </button>
                                            </div>
                                            {#if sess.PermissionRules.length === 0}
                                                <div class="text-xs text-text-muted italic py-1">No rules.</div>
                                            {:else}
                                                <ul class="space-y-1">
                                                    {#each sess.PermissionRules as r, ri (ri)}
                                                        <li class="grid grid-cols-[120px_1fr_120px_auto] gap-2 items-center">
                                                            <input
                                                                type="text"
                                                                bind:value={r.Tool}
                                                                placeholder="Bash"
                                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                                            <input
                                                                type="text"
                                                                bind:value={r.Pattern}
                                                                placeholder="cargo *"
                                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                                            <select
                                                                bind:value={r.Decision}
                                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                                                <option value="allow">allow</option>
                                                                <option value="deny">deny</option>
                                                                <option value="ask">ask</option>
                                                            </select>
                                                            <button
                                                                type="button"
                                                                on:click={() => removePermissionRule(ri)}
                                                                class="px-1.5 py-1 text-xs text-text-muted hover:text-status-error">✕</button>
                                                        </li>
                                                    {/each}
                                                </ul>
                                            {/if}
                                        </div>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">Lifecycle</h3>
                                        <div class="grid grid-cols-2 gap-3">
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Max budget USD (0 = unlimited)
                                                <input
                                                    type="number"
                                                    step="0.01"
                                                    min="0"
                                                    bind:value={sess.MaxBudgetUSD}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Max tasks (0 = unlimited)
                                                <input
                                                    type="number"
                                                    min="0"
                                                    bind:value={sess.MaxTasks}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                            <div class="flex flex-col gap-2">
                                                <label class="flex items-center gap-2 text-sm text-text">
                                                    <input type="checkbox" bind:checked={sess.AutoRestart} />
                                                    Auto-restart after task
                                                </label>
                                                <label class="flex items-center gap-2 text-sm text-text">
                                                    <input type="checkbox" bind:checked={sess.StopWhenNoTasks} />
                                                    Stop when no tasks
                                                </label>
                                                <label class="flex items-center gap-2 text-sm text-text">
                                                    <input type="checkbox" bind:checked={sess.UseWorktree} />
                                                    Use git worktree
                                                </label>
                                            </div>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Pre-flight
                                                <select
                                                    bind:value={sess.Preflight}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                                    <option value="always">always</option>
                                                    <option value="auto">auto</option>
                                                    <option value="never">never</option>
                                                </select>
                                            </label>
                                        </div>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">Hooks</h3>
                                        <div class="grid grid-cols-2 gap-3">
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Pre-task hook
                                                <input
                                                    type="text"
                                                    bind:value={sess.PreTaskHook}
                                                    placeholder="git fetch origin"
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                Post-task hook
                                                <input
                                                    type="text"
                                                    bind:value={sess.PostTaskHook}
                                                    placeholder="cargo clippy -- -D warnings"
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                            </label>
                                        </div>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">Crash recovery</h3>
                                        <label class="flex flex-col text-xs text-text-muted gap-1 mb-3">
                                            Recovery prompt
                                            <textarea
                                                bind:value={sess.CrashRecoveryPrompt}
                                                rows="3"
                                                placeholder="(default: check git status and continue the interrupted task)"
                                                title="Message sent to Claude when resuming an interrupted session. Leave empty to use the built-in default."
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                            ></textarea>
                                        </label>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">Context</h3>
                                        <label class="flex flex-col text-xs text-text-muted gap-1 mb-3">
                                            Append to system prompt
                                            <textarea
                                                bind:value={sess.SystemPromptAppend}
                                                rows="2"
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                            ></textarea>
                                        </label>
                                        <label class="flex flex-col text-xs text-text-muted gap-1">
                                            Additional dirs (one per line)
                                            <textarea
                                                rows="2"
                                                value={joinList(sess.AddDirs)}
                                                on:input={onAddDirsInput}
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                            ></textarea>
                                        </label>
                                    </section>
                                </div>
                            {/if}
                        </div>
                    </div>
                {/if}
            {:else if activeTab === 'workers'}
                <!-- ───────── WORKERS TAB ───────── -->
                <div class="grid grid-cols-[200px_1fr] gap-4 h-full">
                    <!-- Left rail: worker list -->
                    <div class="flex flex-col gap-3 min-h-0">
                        <div class="flex items-center justify-between">
                            <span class="text-text-muted text-xs">Workers</span>
                            <button
                                type="button"
                                data-testid="add-worker"
                                on:click={addWorker}
                                class="px-2 py-0.5 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white">
                                + Add
                            </button>
                        </div>

                        <ul class="border border-bg-border rounded divide-y divide-bg-border bg-bg-elevated overflow-y-auto">
                            {#if cfg.Workers.length === 0}
                                <li class="px-2 py-2 text-xs text-text-muted italic">No workers.</li>
                            {/if}
                            {#each cfg.Workers as w, i (i)}
                                <li class="flex items-center">
                                    <button
                                        type="button"
                                        on:click={() => (selectedWorkerIdx = i)}
                                        class="flex-1 text-left px-2 py-1 text-sm truncate
                                            {selectedWorkerIdx === i
                                                ? 'bg-bg text-text'
                                                : 'text-text-muted hover:text-text hover:bg-bg/50'}">
                                        {w.Name || `(worker ${i + 1})`}
                                        <span class="text-text-dim text-xs">· {w.Role}</span>
                                    </button>
                                    <button
                                        type="button"
                                        on:click={() => removeWorker(i)}
                                        title="Remove"
                                        class="px-1.5 py-1 text-xs text-text-muted hover:text-status-error">✕</button>
                                </li>
                            {/each}
                        </ul>

                        <div class="flex flex-col gap-1">
                            <span class="text-text-muted text-xs">Add from preset</span>
                            {#each workerPresets as preset, pi}
                                <button
                                    type="button"
                                    on:click={() => addWorkerPreset(pi)}
                                    class="px-2 py-1 text-xs rounded bg-bg border border-bg-border text-text-muted hover:text-text hover:border-text-muted text-left">
                                    + {preset.label}
                                </button>
                            {/each}
                        </div>
                    </div>

                    <!-- Right pane: worker editor -->
                    <div class="min-h-0 overflow-y-auto pr-1">
                        {#if !wrk}
                            <div class="text-text-muted text-sm italic py-6 text-center">
                                Select or add a worker.
                            </div>
                        {:else}
                            <div class="space-y-4" data-testid="worker-editor">
                                <div class="grid grid-cols-2 gap-3">
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Name
                                        <input
                                            type="text"
                                            bind:value={wrk.Name}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                    </label>
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Role
                                        <select
                                            bind:value={wrk.Role}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                            <option value="hands">hands</option>
                                            <option value="quality">quality</option>
                                            <option value="eyes">eyes</option>
                                        </select>
                                    </label>
                                </div>
                                <label class="flex flex-col text-xs text-text-muted gap-1">
                                    Base URL (OpenAI-compatible gateway)
                                    <input
                                        type="text"
                                        bind:value={wrk.BaseURL}
                                        placeholder="https://api.kilo.ai/api/gateway"
                                        class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                </label>
                                <div class="grid grid-cols-2 gap-3">
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Model id
                                        <input
                                            type="text"
                                            bind:value={wrk.Model}
                                            placeholder="stepfun/step-3.7-flash:free"
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                    </label>
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        API key env var
                                        <input
                                            type="text"
                                            bind:value={wrk.KeyEnv}
                                            placeholder="KILO_API_KEY"
                                            title="The API key is read from this environment variable — never stored in config."
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                    </label>
                                </div>
                                <div class="grid grid-cols-3 gap-3">
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Reasoning effort
                                        <select
                                            bind:value={wrk.ReasoningEffort}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                            <option value="none">none</option>
                                            <option value="low">low</option>
                                            <option value="medium">medium</option>
                                            <option value="high">high</option>
                                        </select>
                                    </label>
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Max output tokens
                                        <input
                                            type="number"
                                            min="0"
                                            bind:value={wrk.MaxOutputTokens}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                    </label>
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Continuation cap
                                        <input
                                            type="number"
                                            min="0"
                                            bind:value={wrk.ContinuationCap}
                                            title="Max finish_reason=length continuations before giving up (loop guard)."
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                    </label>
                                </div>
                                <div class="grid grid-cols-2 gap-3 items-end">
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        Request timeout (sec)
                                        <input
                                            type="number"
                                            min="0"
                                            bind:value={wrk.RequestTimeoutSec}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                    </label>
                                    <label class="flex items-center gap-2 text-sm text-text mb-1"
                                           title="Warn when generating a brief that its FIND anchors must be pure ASCII (Cyrillic anchors break some models).">
                                        <input type="checkbox" bind:checked={wrk.ASCIIAnchorsOnly} />
                                        ASCII-only FIND anchors
                                    </label>
                                </div>
                            </div>
                        {/if}
                    </div>
                </div>
            {/if}
        </div>

        <!-- Footer -->
        <div class="px-4 py-3 border-t border-bg-border flex items-center justify-between shrink-0">
            <div class="text-xs">
                {#if error}
                    <span class="text-status-error">{error}</span>
                {:else if info}
                    <span class="text-status-working">{info}</span>
                {:else}
                    <span class="text-text-muted">Changes are written to TOML on Save.</span>
                {/if}
            </div>
            <div class="flex gap-2">
                <button
                    type="button"
                    on:click={close}
                    class="px-3 py-1 text-sm rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg">
                    Close
                </button>
                <button
                    type="button"
                    on:click={save}
                    disabled={saving || loading || !cfg}
                    class="px-3 py-1 text-sm rounded font-medium bg-blue-600 hover:bg-blue-500 text-white
                           disabled:opacity-50 disabled:cursor-not-allowed">
                    {saving ? 'Saving…' : 'Save'}
                </button>
            </div>
        </div>
    </div>
</div>

{#if roadmapPlan}
    <PlanReview plan={roadmapPlan} mode="roadmap" on:cancel={onRoadmapCancel} on:roadmapWritten={onRoadmapWritten} />
{/if}
