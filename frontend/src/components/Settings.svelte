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
        InstallSessionProtocol,
    } from '../../wailsjs/go/main/App';
    import { EventsOn } from '../../wailsjs/runtime/runtime';
    import { initProjects } from '../stores/projects';
    import { refreshSessions } from '../stores/sessions';
    import { setTheme, type Theme } from '../stores/theme';
    import { setLocale, t, type Locale } from '../lib/i18n';
    import { formatBytes } from '../lib/formatters';
    import { MODELS, EFFORTS, isKnownModel } from '../lib/models';
    import PlanReview from './PlanReview.svelte';

    const dispatch = createEventDispatcher();

    type Tab = 'global' | 'projects' | 'sessions' | 'workers';
    export let initialTab: Tab = 'global';
    export let initialAction: 'add' | undefined = undefined;
    let activeTab: Tab = initialTab;

    const tabs: { id: Tab; labelKey: string }[] = [
        { id: 'global', labelKey: 'settings.tab.global' },
        { id: 'projects', labelKey: 'settings.tab.projects' },
        { id: 'sessions', labelKey: 'settings.tab.sessions' },
        { id: 'workers', labelKey: 'settings.tab.workers' },
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
        Language: string;
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
        ExperienceTracking: boolean;
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
            Language: 'en',
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
            ExperienceTracking: false,
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
            // Apply persisted theme/language from config to the live stores so
            // the UI matches the saved values when the dialog opens.
            const themeVal = cfg.Settings.Theme === 'light' ? 'light' : 'dark';
            setTheme(themeVal as Theme);
            setLocale(cfg.Settings.Language === 'ru' ? 'ru' : 'en');
            clampSelections();
            if (initialAction === 'add') {
                addProject();
            }
            loadAllProjectLogInfo();
            loadAllRoadmapDrafts();
        } catch (e: any) {
            error = $t('settings.msg.loadFailed', { error: e?.message ?? String(e) });
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

    function onLanguageChange(e: Event) {
        const v = (e.currentTarget as HTMLSelectElement).value as Locale;
        if (gs) gs.Language = v;
        setLocale(v);
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
            const chosen = await PickDirectory($t('settings.msg.selectProjectFolder'));
            if (chosen && cfg) {
                cfg.Projects[idx].Path = chosen;
                cfg = cfg; // trigger reactivity
            }
        } catch (e: any) {
            error = $t('settings.msg.folderPickerFailed', { error: e?.message ?? String(e) });
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
            error = $t('settings.msg.roadmapGenFailed', { error: e?.message ?? String(e) });
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
        info = $t('settings.msg.roadmapWritten');
        await load();
    }

    // ---- Project log files (auto-saved on task/run completion — see
    // CLAUDE.md "Automatic Log Saving") ----
    let projectLogInfo: Record<number, { count: number; size: number } | null> = {};
    // Developer-session protocol (docs/git-workflow.md + the two skills). A
    // roadmap-generated project already has it; this is the retrofit path for
    // projects that predate it or whose queue was written by hand.
    let protocolBusy: number | null = null;
    let protocolResult: Record<number, string> = {};

    async function installProtocol(idx: number) {
        if (!cfg) return;
        const p = cfg.Projects[idx];
        protocolBusy = idx;
        error = '';
        try {
            const files = await InstallSessionProtocol(p.Name);
            protocolResult[idx] = (files ?? []).length
                ? $t('settings.msg.installedFiles', { files: (files ?? []).join(', ') })
                : $t('settings.msg.alreadyPresent');
            protocolResult = protocolResult;
            info = $t('settings.msg.protocolChecked', { project: p.Name });
        } catch (e: any) {
            error = $t('settings.msg.installProtocolFailed', { error: e?.message ?? String(e) });
        } finally {
            protocolBusy = null;
        }
    }

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
            info = $t('settings.msg.clearedLogs', { project: p.Name });
        } catch (e: any) {
            error = $t('settings.msg.clearLogsFailed', { error: e?.message ?? String(e) });
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
            info = $t('settings.msg.saved');
            // Re-pull so we see canonicalised values.
            await load();
        } catch (e: any) {
            error = $t('settings.msg.saveFailed', { error: e?.message ?? String(e) });
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
            <h2 class="text-text font-semibold text-base">{$t('settings.title')}</h2>
            <button
                class="text-text-muted hover:text-text text-sm px-2 py-0.5"
                on:click={close}
                type="button">✕</button>
        </div>

        <!-- Tabs -->
        <div class="px-4 pt-3 border-b border-bg-border flex gap-1 shrink-0">
            {#each tabs as tab}
                <button
                    type="button"
                    class="px-3 py-1.5 text-sm rounded-t border-b-2 transition-colors
                        {activeTab === tab.id
                            ? 'text-text border-blue-500 bg-bg-elevated'
                            : 'text-text-muted border-transparent hover:text-text hover:bg-bg-elevated/50'}"
                    on:click={() => (activeTab = tab.id)}>{$t(tab.labelKey)}</button>
            {/each}
        </div>

        <!-- Body -->
        <div class="flex-1 min-h-0 overflow-y-auto px-4 py-4">
            {#if loading}
                <div class="text-text-muted text-sm italic py-10 text-center">{$t('settings.loadingConfig')}</div>
            {:else if !cfg || !gs}
                <div class="text-status-error text-sm py-10 text-center">{$t('settings.noConfigLoaded')}</div>
            {:else if activeTab === 'global'}
                <!-- ───────── GLOBAL TAB ───────── -->
                <div class="space-y-6">
                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.general.heading')}</h3>
                        <div class="grid grid-cols-2 gap-3">
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.general.claudePath')}
                                <input
                                    type="text"
                                    bind:value={gs.ClaudePath}
                                    placeholder="claude"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.general.theme')}
                                <select
                                    value={gs.Theme}
                                    on:change={onThemeChange}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                    <option value="dark">dark</option>
                                    <option value="light">light</option>
                                </select>
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.language')}
                                <select
                                    value={gs.Language}
                                    on:change={onLanguageChange}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                    <option value="en">English</option>
                                    <option value="ru">Русский</option>
                                </select>
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.general.retryDelay')}
                                <input
                                    type="number"
                                    bind:value={gs.DefaultRetryDelay}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.general.rateLimitPause')}
                                <input
                                    type="number"
                                    bind:value={gs.RateLimitPause}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.general.logRetention')}
                                <input
                                    type="number"
                                    bind:value={gs.LogRetentionDays}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.general.sessionStartDelay')}
                                <input
                                    type="number"
                                    bind:value={gs.SessionStartDelay}
                                    min="0"
                                    title={$t('settings.general.sessionStartDelayTitle')}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                        </div>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.crashRecoveryGlobal.heading')}</h3>
                        <div class="space-y-2">
                            <label class="flex items-center gap-2 text-sm text-text">
                                <input type="checkbox" bind:checked={gs.CrashRecovery} />
                                {$t('settings.crashRecoveryGlobal.enable')}
                            </label>
                            <p class="text-xs text-text-muted leading-relaxed">
                                {$t('settings.crashRecoveryGlobal.desc1')}
                                <code class="bg-bg px-1 rounded">~/.claude-manager/state/</code>
                                {$t('settings.crashRecoveryGlobal.desc2')}
                                <code class="bg-bg px-1 rounded">--resume</code>.
                                {$t('settings.crashRecoveryGlobal.useForceNew1')} <b>{$t('settings.crashRecoveryGlobal.forceNew')}</b>
                                {$t('settings.crashRecoveryGlobal.useForceNew2')}
                            </p>
                        </div>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.preflight.heading')}</h3>
                        <div class="grid grid-cols-2 gap-3">
                            <label class="flex items-center gap-2 text-sm text-text">
                                <input type="checkbox" bind:checked={gs.PreflightAnalysis} />
                                {$t('settings.preflight.enable')}
                            </label>
                            <label class="flex items-center gap-2 text-sm text-text">
                                <input type="checkbox" bind:checked={gs.PreflightAutoApproveSingle} />
                                {$t('settings.preflight.autoApproveSingle')}
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.preflight.analystModel')}
                                <input
                                    type="text"
                                    bind:value={gs.PreflightModel}
                                    placeholder="haiku"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.preflight.maxBudget')}
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
                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.permTimeouts.heading')}</h3>
                        <div class="grid grid-cols-2 gap-3">
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.permTimeouts.notifyAfter')}
                                <input
                                    type="number"
                                    bind:value={gs.PermissionNotifyAfter}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.permTimeouts.timeout')}
                                <input
                                    type="number"
                                    bind:value={gs.PermissionTimeout}
                                    min="0"
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.permTimeouts.timeoutAction')}
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
                                    {$t('settings.permTimeouts.nativeNotification')}
                                </label>
                                <label class="flex items-center gap-2 text-sm text-text">
                                    <input type="checkbox" bind:checked={gs.PermissionSound} />
                                    {$t('settings.permTimeouts.soundAlert')}
                                </label>
                            </div>
                        </div>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.modelRouting.heading')}</h3>
                        <label class="flex items-center gap-2 text-sm text-text">
                            <input type="checkbox" bind:checked={cfg.Optimization.AutoModelRouting} />
                            {$t('settings.modelRouting.autoRouting')}
                        </label>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.experienceLayer.heading')}</h3>
                        <label class="flex items-center gap-2 text-sm text-text">
                            <input type="checkbox" bind:checked={cfg.Optimization.ExperienceTracking} />
                            {$t('settings.experienceLayer.enable')}
                        </label>
                        <p class="text-text-dim text-xs mt-1">
                            {$t('settings.experienceLayer.description')}
                        </p>
                    </section>

                    <section>
                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.budgetAlerts.heading')}</h3>
                        <div class="grid grid-cols-3 gap-3">
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.budgetAlerts.daily')}
                                <input
                                    type="number"
                                    step="0.01"
                                    min="0"
                                    bind:value={gs.DailyBudgetAlert}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.budgetAlerts.weekly')}
                                <input
                                    type="number"
                                    step="0.01"
                                    min="0"
                                    bind:value={gs.WeeklyBudgetAlert}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                            </label>
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.budgetAlerts.rateLimitThreshold')}
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
                            {cfg.Projects.length === 1
                                ? $t('settings.projects.countSingular', { count: cfg.Projects.length })
                                : $t('settings.projects.countPlural', { count: cfg.Projects.length })}
                        </span>
                        <button
                            type="button"
                            on:click={addProject}
                            class="px-2 py-1 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white">
                            {$t('settings.projects.addProjectButton')}
                        </button>
                    </div>

                    {#if cfg.Projects.length === 0}
                        <div class="text-text-muted text-sm italic py-6 text-center">
                            {$t('settings.projects.emptyPart1')} <b>{$t('settings.projects.addProjectLabel')}</b> {$t('settings.projects.emptyPart2')}
                        </div>
                    {:else}
                        <ul class="space-y-3">
                            {#each cfg.Projects as p, i (i)}
                                <li class="bg-bg-elevated border border-bg-border rounded p-3 space-y-2">
                                    <div class="flex items-center justify-between">
                                        <span class="text-text-muted text-xs">{$t('settings.projects.projectNumber', { n: i + 1 })}</span>
                                        <button
                                            type="button"
                                            on:click={() => removeProject(i)}
                                            class="px-2 py-0.5 text-xs rounded
                                                   bg-status-error/80 hover:bg-status-error text-white">
                                            {$t('settings.common.remove')}
                                        </button>
                                    </div>

                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.field.name')}
                                        <input
                                            type="text"
                                            bind:value={p.Name}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                    </label>

                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.projects.path')}
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
                                                {$t('settings.projects.browse')}
                                            </button>
                                        </div>
                                        {#if p.Path}
                                            <span class="text-[11px] text-text-muted/80 leading-snug" data-testid={`storage-note-${i}`}>
                                                {$t('settings.projects.storageNote1')}
                                                <code class="font-mono">{p.Path}\.claude-manager\config.toml</code>
                                                {$t('settings.projects.storageNote2')} <code class="font-mono">config.local.toml</code>
                                                {$t('settings.projects.storageNote3')}
                                            </span>
                                        {:else}
                                            <span class="text-[11px] text-text-muted/60 leading-snug">
                                                {$t('settings.projects.storageNoteNoPath')}
                                            </span>
                                        {/if}
                                    </label>

                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.projects.defaultPermMode')}
                                        <select
                                            bind:value={p.DefaultPermissionMode}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                            <option value="">{$t('settings.projects.inheritOption')}</option>
                                            <option value="bypassPermissions">bypassPermissions</option>
                                            <option value="acceptEdits">acceptEdits</option>
                                            <option value="default">default</option>
                                            <option value="auto">auto</option>
                                            <option value="plan">plan</option>
                                            <option value="dontAsk">dontAsk</option>
                                        </select>
                                        <span class="text-[11px] text-text-muted/70 leading-snug">
                                            {$t('settings.projects.defaultPermModeHint')}
                                        </span>
                                    </label>

                                    <div class="flex items-center justify-between">
                                        <span class="text-xs text-text-muted">
                                            {p.Sessions.length === 1
                                                ? $t('settings.projects.sessionCountSingular', { count: p.Sessions.length })
                                                : $t('settings.projects.sessionCountPlural', { count: p.Sessions.length })}
                                        </span>
                                        <button
                                            type="button"
                                            on:click={() => { selectedProjectIdx = i; activeTab = 'sessions'; }}
                                            class="px-2 py-0.5 text-xs rounded bg-bg border border-bg-border text-text-muted hover:text-text hover:border-text-muted">
                                            {$t('settings.projects.addSessionArrow')}
                                        </button>
                                    </div>

                                    <!-- Mixed programming (MIXED-TASKS.md) -->
                                    <div class="border-t border-bg-border pt-2 mt-1 space-y-2">
                                        <label class="flex items-center gap-2 text-sm text-text">
                                            <input
                                                type="checkbox"
                                                data-testid={`mixed-enable-${i}`}
                                                bind:checked={p.MixedProgramming} />
                                            {$t('settings.mixed.enable')}
                                        </label>
                                        {#if p.MixedProgramming}
                                            <p class="text-xs text-status-error/90 leading-relaxed">
                                                {$t('settings.mixed.warning1')}
                                                <span class="text-text-muted">{$t('settings.mixed.warning2a')}
                                                <code class="font-mono">config.local.toml</code>
                                                {$t('settings.mixed.warning2b')}</span>
                                            </p>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.mixed.gatesLabel')}
                                                <textarea
                                                    rows="2"
                                                    value={joinList(p.Gates)}
                                                    on:input={(e) => onProjectGatesInput(e, i)}
                                                    placeholder="go build ./...&#10;go test ./..."
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                                ></textarea>
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1 w-40">
                                                {$t('settings.mixed.maxRounds')}
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
                                            {$t('settings.roadmap.intro1')}
                                            <code class="font-mono">ROADMAP.md</code> +
                                            <code class="font-mono">STATUS-P1.md</code>
                                            {$t('settings.roadmap.intro2')}
                                        </div>
                                        {#if roadmapDraft[i] && !roadmapDraftDismissed.has(i) && roadmapBusy !== i}
                                            <div class="flex items-center justify-between gap-2 text-xs bg-amber-500/10
                                                        border border-amber-500/40 rounded px-2 py-1.5">
                                                <span class="text-text">
                                                    {$t('settings.roadmap.foundDraft', { count: roadmapDraft[i].subtasks?.length ?? 0 })}
                                                </span>
                                                <div class="flex items-center gap-1 shrink-0">
                                                    <button
                                                        type="button"
                                                        on:click={() => openRecoveredDraft(i)}
                                                        class="px-2 py-0.5 rounded bg-amber-600 hover:bg-amber-500 text-white">
                                                        {$t('settings.roadmap.reviewButton')}
                                                    </button>
                                                    <button
                                                        type="button"
                                                        on:click={() => dismissRoadmapDraft(i)}
                                                        class="px-2 py-0.5 rounded bg-bg border border-bg-border
                                                               text-text-muted hover:text-text">
                                                        {$t('settings.roadmap.dismissButton')}
                                                    </button>
                                                </div>
                                            </div>
                                        {/if}
                                        <label class="flex flex-col text-xs text-text-muted gap-1">
                                            {$t('settings.roadmap.ideaLabel')}
                                            <textarea
                                                rows="3"
                                                bind:value={roadmapIdea[i]}
                                                disabled={!p.Path}
                                                placeholder={$t('settings.roadmap.ideaPlaceholder')}
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
                                                        {m.label}{m.value === 'opus' ? $t('settings.roadmap.recommendedSuffix') : ''}
                                                    </option>
                                                {/each}
                                            </select>
                                            <button
                                                type="button"
                                                on:click={() => generateRoadmapForProject(i)}
                                                disabled={!p.Path || !(roadmapIdea[i] ?? '').trim() || roadmapBusy !== null}
                                                class="px-2 py-1 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white
                                                       disabled:opacity-50 disabled:cursor-not-allowed">
                                                {roadmapBusy === i ? $t('settings.roadmap.generatingButton', { elapsed: roadmapElapsedText }) : $t('settings.roadmap.generateButton')}
                                            </button>
                                        </div>
                                        {#if roadmapBusy === i}
                                            <div class="text-[11px] text-text-muted font-mono truncate" title={roadmapProgress[i] ?? ''}>
                                                {roadmapProgress[i] ? roadmapProgress[i] : $t('settings.roadmap.startingAnalyst')}
                                            </div>
                                        {/if}
                                        {#if !p.Path}
                                            <span class="text-[11px] text-text-muted/60">
                                                {$t('settings.roadmap.setPathFirst')}
                                            </span>
                                        {/if}
                                    </div>

                                    <!-- Saved session logs (auto-saved on task/run completion into
                                         <project>/.claude-manager/logs/) -->
                                    <div class="border-t border-bg-border pt-2 mt-1 space-y-2">
                                        <div class="flex items-center justify-between gap-2">
                                            <span class="text-xs text-text-muted">
                                                {#if !p.Path}
                                                    {$t('settings.logs.setPathFirst')}
                                                {:else if projectLogInfo[i]}
                                                    {(projectLogInfo[i]?.count ?? 0) === 1
                                                        ? $t('settings.logs.countSingular', { count: projectLogInfo[i]?.count ?? 0, size: formatBytes(projectLogInfo[i]?.size) })
                                                        : $t('settings.logs.countPlural', { count: projectLogInfo[i]?.count ?? 0, size: formatBytes(projectLogInfo[i]?.size) })}
                                                {:else}
                                                    {$t('settings.logs.unknown')}
                                                {/if}
                                            </span>
                                            <div class="flex items-center gap-1">
                                                <button
                                                    type="button"
                                                    on:click={() => loadProjectLogInfo(i)}
                                                    disabled={!p.Path}
                                                    title={$t('settings.logs.refreshTitle')}
                                                    class="px-1.5 py-0.5 text-xs rounded bg-bg border border-bg-border
                                                           text-text-muted hover:text-text disabled:opacity-40 disabled:cursor-not-allowed">
                                                    🔄
                                                </button>
                                                <button
                                                    type="button"
                                                    on:click={() => requestClearProjectLogs(i)}
                                                    disabled={!p.Path || !projectLogInfo[i]?.count || logsBusy !== null}
                                                    title={logsPendingClear === i
                                                        ? $t('settings.logs.confirmDeleteTitle')
                                                        : $t('settings.logs.deleteTitle')}
                                                    class="px-2 py-0.5 text-xs rounded border disabled:opacity-40 disabled:cursor-not-allowed
                                                           {logsPendingClear === i
                                                               ? 'bg-status-error/20 border-status-error/50 text-status-error font-semibold'
                                                               : 'bg-bg border-bg-border text-text-muted hover:text-status-error'}">
                                                    {#if logsBusy === i}…{:else if logsPendingClear === i}{$t('settings.logs.confirmClearButton')}{:else}{$t('settings.logs.clearButton')}{/if}
                                                </button>
                                            </div>
                                        </div>
                                        <p class="text-[11px] text-text-muted/70 leading-snug">
                                            {$t('settings.logs.description')}
                                        </p>
                                    </div>
                                    <div class="border-t border-bg-border pt-2 mt-1 space-y-1">
                                        <div class="flex items-center justify-between gap-2">
                                            <span class="text-xs text-text-muted">{$t('settings.protocol.label')}</span>
                                            <button
                                                type="button"
                                                on:click={() => installProtocol(i)}
                                                disabled={!p.Path || protocolBusy !== null}
                                                title={$t('settings.protocol.installTitle')}
                                                class="px-2 py-0.5 text-xs rounded bg-bg border border-bg-border
                                                       text-text-muted hover:text-text disabled:opacity-40 disabled:cursor-not-allowed">
                                                {#if protocolBusy === i}…{:else}{$t('settings.protocol.installButton')}{/if}
                                            </button>
                                        </div>
                                        {#if protocolResult[i]}
                                            <p class="text-[11px] text-status-ok/90 leading-snug">{protocolResult[i]}</p>
                                        {/if}
                                        <p class="text-[11px] text-text-muted/70 leading-snug">
                                            {$t('settings.protocol.description1')} <code>--no-ff</code>
                                            {$t('settings.protocol.description2')}
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
                        {$t('settings.sessions.addProjectFirst')}
                    </div>
                {:else}
                    <div class="grid grid-cols-[200px_1fr] gap-4 h-full">
                        <!-- Left rail: project + session pickers -->
                        <div class="flex flex-col gap-3 min-h-0">
                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                {$t('settings.sessions.projectLabel')}
                                <select
                                    bind:value={selectedProjectIdx}
                                    on:change={() => (selectedSessionIdx = 0)}
                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                    {#each cfg.Projects as p, i}
                                        <option value={i}>{p.Name || $t('settings.sessions.projectFallback', { n: i + 1 })}</option>
                                    {/each}
                                </select>
                            </label>

                            <div class="flex items-center justify-between">
                                <span class="text-text-muted text-xs">{$t('settings.sessions.listHeading')}</span>
                                <button
                                    type="button"
                                    on:click={addSession}
                                    class="px-2 py-0.5 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white">
                                    {$t('settings.sessions.addButton')}
                                </button>
                            </div>

                            <ul class="border border-bg-border rounded divide-y divide-bg-border bg-bg-elevated overflow-y-auto">
                                {#if proj && proj.Sessions.length === 0}
                                    <li class="px-2 py-2 text-xs text-text-muted italic">{$t('settings.sessions.noSessions')}</li>
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
                                            {s.Name || $t('settings.sessions.sessionFallback', { n: i + 1 })}
                                        </button>
                                        <button
                                            type="button"
                                            on:click={() => removeSession(i)}
                                            title={$t('settings.common.remove')}
                                            class="px-1.5 py-1 text-xs text-text-muted hover:text-status-error">✕</button>
                                    </li>
                                {/each}
                            </ul>
                        </div>

                        <!-- Right pane: session editor -->
                        <div class="min-h-0 overflow-y-auto pr-1">
                            {#if !sess}
                                <div class="text-text-muted text-sm italic py-6 text-center">
                                    {$t('settings.sessions.selectOrAdd')}
                                </div>
                            {:else}
                                <div class="space-y-5">
                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.sessions.identityHeading')}</h3>
                                        <div class="grid grid-cols-2 gap-3">
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.field.name')}
                                                <input
                                                    type="text"
                                                    bind:value={sess.Name}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.sessions.taskSourceFile')}
                                                <input
                                                    type="text"
                                                    bind:value={sess.TaskSource}
                                                    placeholder="STATUS-P1.md"
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                        </div>
                                        <label class="flex flex-col text-xs text-text-muted gap-1 mt-3">
                                            {$t('settings.sessions.promptLabel')}
                                            <textarea
                                                bind:value={sess.Prompt}
                                                rows="6"
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                            ></textarea>
                                        </label>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.sessions.modelHeading')}</h3>
                                        <div class="grid grid-cols-3 gap-3">
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.field.model')}
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
                                                {$t('settings.sessions.effort')}
                                                <select
                                                    bind:value={sess.Effort}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                                    {#each EFFORTS as e}
                                                        <option value={e}>{e}</option>
                                                    {/each}
                                                </select>
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.sessions.fallbackModel')}
                                                <input
                                                    type="text"
                                                    bind:value={sess.FallbackModel}
                                                    placeholder="haiku"
                                                    title={$t('settings.sessions.fallbackModelTitle')}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                        </div>
                                        <label class="flex items-center gap-2 text-sm text-text mt-3"
                                               title={$t('settings.sessions.fallbackToggleTitle')}>
                                            <input type="checkbox" bind:checked={sess.FallbackModelOnRateLimit} />
                                            {$t('settings.sessions.fallbackToggleLabel')}
                                        </label>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.sessions.permissionsHeading')}</h3>
                                        <label class="flex flex-col text-xs text-text-muted gap-1 mb-3">
                                            {$t('settings.sessions.permissionMode')}
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
                                                {$t('settings.sessions.allowedTools')}
                                                <textarea
                                                    rows="3"
                                                    value={joinList(sess.AllowedTools)}
                                                    on:input={onAllowedToolsInput}
                                                    placeholder="Bash(cargo:*)&#10;Bash(npm:*)"
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                                ></textarea>
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.sessions.disallowedTools')}
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
                                                <span class="text-text-muted text-xs">{$t('settings.sessions.autoApproveRules')}</span>
                                                <button
                                                    type="button"
                                                    on:click={addPermissionRule}
                                                    class="px-2 py-0.5 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white">
                                                    {$t('settings.sessions.addRule')}
                                                </button>
                                            </div>
                                            {#if sess.PermissionRules.length === 0}
                                                <div class="text-xs text-text-muted italic py-1">{$t('settings.sessions.noRules')}</div>
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
                                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.sessions.lifecycleHeading')}</h3>
                                        <div class="grid grid-cols-2 gap-3">
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.sessions.maxBudget')}
                                                <input
                                                    type="number"
                                                    step="0.01"
                                                    min="0"
                                                    bind:value={sess.MaxBudgetUSD}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.sessions.maxTasks')}
                                                <input
                                                    type="number"
                                                    min="0"
                                                    bind:value={sess.MaxTasks}
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                            </label>
                                            <div class="flex flex-col gap-2">
                                                <label class="flex items-center gap-2 text-sm text-text">
                                                    <input type="checkbox" bind:checked={sess.AutoRestart} />
                                                    {$t('settings.sessions.autoRestart')}
                                                </label>
                                                <label class="flex items-center gap-2 text-sm text-text">
                                                    <input type="checkbox" bind:checked={sess.StopWhenNoTasks} />
                                                    {$t('settings.sessions.stopWhenNoTasks')}
                                                </label>
                                                <label class="flex items-center gap-2 text-sm text-text">
                                                    <input type="checkbox" bind:checked={sess.UseWorktree} />
                                                    {$t('settings.sessions.useWorktree')}
                                                </label>
                                            </div>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.sessions.preflightLabel')}
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
                                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.sessions.hooksHeading')}</h3>
                                        <div class="grid grid-cols-2 gap-3">
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.sessions.preTaskHook')}
                                                <input
                                                    type="text"
                                                    bind:value={sess.PreTaskHook}
                                                    placeholder="git fetch origin"
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                            </label>
                                            <label class="flex flex-col text-xs text-text-muted gap-1">
                                                {$t('settings.sessions.postTaskHook')}
                                                <input
                                                    type="text"
                                                    bind:value={sess.PostTaskHook}
                                                    placeholder="cargo clippy -- -D warnings"
                                                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                            </label>
                                        </div>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.sessions.crashRecoveryHeading')}</h3>
                                        <label class="flex flex-col text-xs text-text-muted gap-1 mb-3">
                                            {$t('settings.sessions.recoveryPrompt')}
                                            <textarea
                                                bind:value={sess.CrashRecoveryPrompt}
                                                rows="3"
                                                placeholder={$t('settings.sessions.recoveryPromptPlaceholder')}
                                                title={$t('settings.sessions.recoveryPromptTitle')}
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                            ></textarea>
                                        </label>
                                    </section>

                                    <section>
                                        <h3 class="text-text font-semibold text-sm mb-2">{$t('settings.sessions.contextHeading')}</h3>
                                        <label class="flex flex-col text-xs text-text-muted gap-1 mb-3">
                                            {$t('settings.sessions.appendSystemPrompt')}
                                            <textarea
                                                bind:value={sess.SystemPromptAppend}
                                                rows="2"
                                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                                            ></textarea>
                                        </label>
                                        <label class="flex flex-col text-xs text-text-muted gap-1">
                                            {$t('settings.sessions.additionalDirs')}
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
                            <span class="text-text-muted text-xs">{$t('settings.workers.listHeading')}</span>
                            <button
                                type="button"
                                data-testid="add-worker"
                                on:click={addWorker}
                                class="px-2 py-0.5 text-xs rounded bg-blue-600 hover:bg-blue-500 text-white">
                                {$t('settings.workers.addButton')}
                            </button>
                        </div>

                        <ul class="border border-bg-border rounded divide-y divide-bg-border bg-bg-elevated overflow-y-auto">
                            {#if cfg.Workers.length === 0}
                                <li class="px-2 py-2 text-xs text-text-muted italic">{$t('settings.workers.noWorkers')}</li>
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
                                        {w.Name || $t('settings.workers.fallback', { n: i + 1 })}
                                        <span class="text-text-dim text-xs">· {w.Role}</span>
                                    </button>
                                    <button
                                        type="button"
                                        on:click={() => removeWorker(i)}
                                        title={$t('settings.common.remove')}
                                        class="px-1.5 py-1 text-xs text-text-muted hover:text-status-error">✕</button>
                                </li>
                            {/each}
                        </ul>

                        <div class="flex flex-col gap-1">
                            <span class="text-text-muted text-xs">{$t('settings.workers.addFromPreset')}</span>
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
                                {$t('settings.workers.selectOrAdd')}
                            </div>
                        {:else}
                            <div class="space-y-4" data-testid="worker-editor">
                                <div class="grid grid-cols-2 gap-3">
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.field.name')}
                                        <input
                                            type="text"
                                            bind:value={wrk.Name}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                    </label>
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.workers.role')}
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
                                    {$t('settings.workers.baseUrl')}
                                    <input
                                        type="text"
                                        bind:value={wrk.BaseURL}
                                        placeholder="https://api.kilo.ai/api/gateway"
                                        class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                </label>
                                <div class="grid grid-cols-2 gap-3">
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.workers.modelId')}
                                        <input
                                            type="text"
                                            bind:value={wrk.Model}
                                            placeholder="stepfun/step-3.7-flash:free"
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                    </label>
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.workers.apiKeyEnv')}
                                        <input
                                            type="text"
                                            bind:value={wrk.KeyEnv}
                                            placeholder="KILO_API_KEY"
                                            title={$t('settings.workers.apiKeyEnvTitle')}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                                    </label>
                                </div>
                                <div class="grid grid-cols-3 gap-3">
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.workers.reasoningEffort')}
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
                                        {$t('settings.workers.maxOutputTokens')}
                                        <input
                                            type="number"
                                            min="0"
                                            bind:value={wrk.MaxOutputTokens}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                    </label>
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.workers.continuationCap')}
                                        <input
                                            type="number"
                                            min="0"
                                            bind:value={wrk.ContinuationCap}
                                            title={$t('settings.workers.continuationCapTitle')}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                    </label>
                                </div>
                                <div class="grid grid-cols-2 gap-3 items-end">
                                    <label class="flex flex-col text-xs text-text-muted gap-1">
                                        {$t('settings.workers.requestTimeout')}
                                        <input
                                            type="number"
                                            min="0"
                                            bind:value={wrk.RequestTimeoutSec}
                                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
                                    </label>
                                    <label class="flex items-center gap-2 text-sm text-text mb-1"
                                           title={$t('settings.workers.asciiAnchorsTitle')}>
                                        <input type="checkbox" bind:checked={wrk.ASCIIAnchorsOnly} />
                                        {$t('settings.workers.asciiAnchorsLabel')}
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
                    <span class="text-text-muted">{$t('settings.footer.changesNote')}</span>
                {/if}
            </div>
            <div class="flex gap-2">
                <button
                    type="button"
                    on:click={close}
                    class="px-3 py-1 text-sm rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg">
                    {$t('common.close')}
                </button>
                <button
                    type="button"
                    on:click={save}
                    disabled={saving || loading || !cfg}
                    class="px-3 py-1 text-sm rounded font-medium bg-blue-600 hover:bg-blue-500 text-white
                           disabled:opacity-50 disabled:cursor-not-allowed">
                    {saving ? $t('settings.footer.saving') : $t('common.save')}
                </button>
            </div>
        </div>
    </div>
</div>

{#if roadmapPlan}
    <PlanReview plan={roadmapPlan} mode="roadmap" on:cancel={onRoadmapCancel} on:roadmapWritten={onRoadmapWritten} />
{/if}
