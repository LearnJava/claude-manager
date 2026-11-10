<script lang="ts">
    import { createEventDispatcher } from 'svelte';
    import { projectGroups, collapsedProjects, toggleProject, initProjects } from '../stores/projects';
    import ModelPicker from './ModelPicker.svelte';
    import ResumePrompt from './ResumePrompt.svelte';
    import SessionFeatures from './SessionFeatures.svelte';
    import PlanReview from './PlanReview.svelte';
    import QueueMiniBar from './QueueMiniBar.svelte';
    import { syncQueue } from '../stores/queues';
    import { MODELS, normalizeModel, providerCombos, type ProviderUI } from '../lib/models';
    import { t } from '../lib/i18n';

    const dispatch = createEventDispatcher();
    import { selectedSessionId, sessions, refreshSessions, type SessionState, type SessionStatus } from '../stores/sessions';
    import {
        StartSession,
        StopSession,
        StartProject,
        StopProject,
        GetConfig,
        UpdateConfig,
        GetAutoModelRouting,
        StartSessionWithModel,
        StartSessionWithProviderAndModel,
        GetProviders,
        SetSessionModel,
        SetSessionModelAndEffort,
        SetSessionProvider,
        SetSessionRuntime,
        HasClaudeMd,
        GenerateClaudeMdSession,
        GetSessionState,
        ClearSessionState,
        SessionPreflight,
    } from '../../wailsjs/go/main/App';

    // Live model switch (small dropdown under each running session's name).
    // Autonomous sessions pick the new model up at the next task boundary;
    // interactive ones are soft-restarted immediately (see SetSessionModel /
    // CLAUDE.md "Live Model Switching").
    //
    // The session's model is normalized for display: once the CLI reports back
    // its resolved id (`claude-sonnet-5`) the raw value no longer matches the
    // alias we sent (`sonnet`), and the select used to grow a second entry for
    // the same model. The raw id stays in the tooltip.
    let modelBusy: Record<string, boolean> = {};
    let modelError: Record<string, string> = {};

    // Providers (backend-configured brains). The session row's first
    // dropdown is "which AI": Claude | Hermes | each provider; the second
    // shows that AI's models — for a provider, the flat model×effort list.
    let providers: ProviderUI[] = [];
    GetProviders()
        .then((ps: ProviderUI[]) => { providers = ps ?? []; })
        .catch(() => { providers = []; });

    function providerOfSession(s: SessionState): string {
        if (s.provider) return s.provider;
        // Fallback while the state predates the field: derive from the model
        // (case-insensitive — the CLI may echo the id back in its own case).
        const m = (s.model ?? '').trim().toLowerCase();
        return providers.find((p) => (p.models ?? []).some((mm) => mm.id.toLowerCase() === m))?.id ?? '';
    }

    // A hermes session with no provider selected: hermes resolves its own
    // default (here: the local Kimi proxy), so Claude CLI aliases are lies —
    // the model list is the hermes-capable providers' combos instead.
    function isPlainHermes(s: SessionState): boolean {
        return s.runtime === 'hermes' && !providerOfSession(s);
    }

    // The provider whose combos a session's model dropdown shows: the
    // selected one, or — for a plain hermes session — the hermes-capable
    // provider matching its current model (else the first hermes-capable).
    function comboProviderOf(s: SessionState): ProviderUI | undefined {
        const sel = providerOfSession(s);
        if (sel) return providers.find((x) => x.id === sel);
        if (s.runtime !== 'hermes') return undefined;
        const hermesCapable = providers.filter((p) => p.hermesProviderId);
        const m = (s.model ?? '').trim().toLowerCase();
        return hermesCapable.find((p) => (p.models ?? []).some((mm) => mm.id.toLowerCase() === m))
            ?? hermesCapable[0];
    }

    function aiValueOf(s: SessionState): string {
        const p = providerOfSession(s);
        return p ? `prov:${p}` : runtimeOf(s);
    }

    function providerCombosFor(s: SessionState): { value: string; label: string; model: string; effort: string }[] {
        const p = comboProviderOf(s);
        if (!p) return [];
        if (providerOfSession(s)) return providerCombos(p);
        // Plain hermes: the union of every hermes-capable provider's combos.
        return providers.filter((x) => x.hermesProviderId).flatMap((x) => providerCombos(x));
    }

    function comboValueOf(s: SessionState): string {
        // Case-insensitive: the CLI may echo the model id back in its own
        // case; the display must never go blank over a spelling difference.
        const m = (modelValue(s) || '').toLowerCase();
        const combos = providerCombosFor(s);
        if (combos.some((c) => c.model.toLowerCase() === m)) {
            return (combos.find((c) => c.model.toLowerCase() === m && c.effort === s.effort)
                ?? combos.find((c) => c.model.toLowerCase() === m)).value;
        }
        // The pinned model is not among the combos: show it verbatim (the
        // matching extra option) instead of silently selecting another.
        if (combos.length > 0) return 'custom';
        return modelValue(s);
    }

    function comboMatchesModel(s: SessionState): boolean {
        const m = (modelValue(s) || '').toLowerCase();
        return providerCombosFor(s).some((c) => c.model.toLowerCase() === m);
    }

    async function onAIChange(e: Event, s: SessionState) {
        e.stopPropagation();
        const v = (e.target as HTMLSelectElement).value;
        if (v.startsWith('prov:')) {
            modelBusy = { ...modelBusy, [s.id]: true };
            modelError = { ...modelError, [s.id]: '' };
            try {
                await SetSessionProvider(s.id, v.slice(5));
                await refreshSessions();
            } catch (err: any) {
                modelError = { ...modelError, [s.id]: err?.message ?? String(err) };
            } finally {
                modelBusy = { ...modelBusy, [s.id]: false };
            }
            return;
        }
        await onRuntimeChange(e, s);
    }

    async function onComboChange(e: Event, s: SessionState) {
        e.stopPropagation();
        const v = (e.target as HTMLSelectElement).value;
        const c = providerCombosFor(s).find((x) => x.value === v);
        if (!c || (c.model.toLowerCase() === modelValue(s).toLowerCase() && c.effort === s.effort)) return;
        modelBusy = { ...modelBusy, [s.id]: true };
        modelError = { ...modelError, [s.id]: '' };
        try {
            // One call for both shapes: a provider session and a plain
            // hermes session picking from the hermes-capable combos —
            // SetSessionModelAndEffort derives the provider from the model
            // id and carries the hermes --provider flag.
            await SetSessionModelAndEffort(s.id, c.model, c.effort);
            sessions.update((map) => {
                const cur = map[s.id];
                if (!cur) return map;
                return { ...map, [s.id]: { ...cur, model: c.model, effort: c.effort } };
            });
        } catch (err: any) {
            modelError = { ...modelError, [s.id]: err?.message ?? String(err) };
        } finally {
            modelBusy = { ...modelBusy, [s.id]: false };
        }
    }

    async function onModelChange(e: Event, s: SessionState) {
        e.stopPropagation();
        const model = (e.target as HTMLSelectElement).value;
        if (!model || model === modelValue(s)) return;
        modelBusy = { ...modelBusy, [s.id]: true };
        modelError = { ...modelError, [s.id]: '' };
        try {
            await SetSessionModel(s.id, model);
            // Optimistic: no dedicated event confirms this, only the next
            // session:init (autonomous: next task; interactive: after the
            // soft-restart) actually reports the resolved model.
            sessions.update((map) => {
                const cur = map[s.id];
                if (!cur) return map;
                return { ...map, [s.id]: { ...cur, model } };
            });
        } catch (err: any) {
            modelError = { ...modelError, [s.id]: err?.message ?? String(err) };
        } finally {
            modelBusy = { ...modelBusy, [s.id]: false };
        }
    }

    // Per-session CLI switch (Claude Code CLI / Hermes CLI). Persisted in the
    // session's config (`runtime`, same field as Settings) by SetSessionRuntime;
    // the store is then re-read from the backend, which reports a stopped
    // session's runtime/model from config — so the row shows what the next
    // start will really run. Locked while the session runs: the CLI is fixed
    // for the lifetime of its process.
    // Switching the runtime also clears a previously selected brain provider:
    // its artifacts (claude settings file vs hermes --provider) do not carry
    // across runtimes, and a stale provider would refuse the next start.
    async function onRuntimeChange(e: Event, s: SessionState) {
        e.stopPropagation();
        const runtime = (e.target as HTMLSelectElement).value;
        if (runtime === runtimeOf(s) && !providerOfSession(s)) return;
        modelBusy = { ...modelBusy, [s.id]: true };
        modelError = { ...modelError, [s.id]: '' };
        try {
            if (providerOfSession(s)) {
                // Leaving a provider brain: reset it so the session runs a
                // plain claude/hermes model again.
                await SetSessionProvider(s.id, '');
            }
            if (runtime !== runtimeOf(s)) {
                await SetSessionRuntime(s.id, runtime);
            }
        } catch (err: any) {
            modelError = { ...modelError, [s.id]: err?.message ?? String(err) };
        } finally {
            await refreshSessions();
            modelBusy = { ...modelBusy, [s.id]: false };
        }
    }

    function runtimeOf(s: SessionState): string {
        return s.runtime === 'hermes' ? 'hermes' : 'claude';
    }

    // Value for the model <select>. Claude sessions fold resolved ids onto
    // our aliases (see lib/models). Hermes takes any provider id
    // (`anthropic/claude-sonnet-4.6`), and folding that onto "sonnet" would
    // show a model the session isn't actually using — only exact aliases fold.
    function modelValue(s: SessionState): string {
        if (s.runtime === 'hermes') {
            const raw = (s.model ?? '').trim();
            return MODELS.some((m) => m.value === raw.toLowerCase()) ? raw.toLowerCase() : raw;
        }
        return normalizeModel(s.model);
    }

    function hasCustomModel(s: SessionState): boolean {
        const v = modelValue(s);
        if (!v || MODELS.some((m) => m.value === v)) return false;
        // Provider models get their own labelled options above — no raw
        // duplicate entry for the same id.
        return !providers.some((p) => (p.models ?? []).some((m) => m.id === v));
    }

    // CLAUDE.md presence, checked lazily per project on first click (not a
    // bulk scan of every configured project at startup). undefined = not
    // checked yet, false = missing → banner shown until generated/dismissed.
    let claudeMdStatus: Record<string, boolean> = {};
    let dismissedClaudeMd = new Set<string>();
    let claudeMdBusy: Record<string, boolean> = {};

    async function checkClaudeMd(path: string, name: string) {
        if (!path || name in claudeMdStatus) return;
        try {
            claudeMdStatus = { ...claudeMdStatus, [name]: await HasClaudeMd(path) };
        } catch (err) {
            console.warn('HasClaudeMd failed:', err);
        }
    }

    function onToggleProjectHeader(group: { name: string; path: string }) {
        toggleProject(group.name);
        checkClaudeMd(group.path, group.name);
    }

    async function onGenerateClaudeMd(e: Event, name: string) {
        e.stopPropagation();
        claudeMdBusy = { ...claudeMdBusy, [name]: true };
        try {
            await GenerateClaudeMdSession(name);
            // The "Init" session row now carries progress; no need for the banner.
            dismissedClaudeMd.add(name);
            dismissedClaudeMd = dismissedClaudeMd;
        } catch (err) {
            console.warn('generate CLAUDE.md failed:', err);
        } finally {
            claudeMdBusy = { ...claudeMdBusy, [name]: false };
        }
    }

    function onDismissClaudeMd(e: Event, name: string) {
        e.stopPropagation();
        dismissedClaudeMd.add(name);
        dismissedClaudeMd = dismissedClaudeMd;
    }

    // Session for which the ModelPicker is currently open: "project/name" or null
    let pickerFor: { project: string; name: string } | null = null;
    // Session whose "Features" popup is open (the ⚙ icon on its row).
    let featuresFor: { project: string; name: string } | null = null;
    // Pre-flight analysis on start (PLAN.md §17, preflight_gate.go):
    // analyzing marks rows whose analysis is running, planFor holds a plan
    // the user has to review before anything starts.
    let analyzing: Record<string, boolean> = {};
    let planFor: { project: string; name: string; plan: any } | null = null;

    // Crash-recovery state per session id. A state file that still carries a CLI
    // session id means the previous run never reported a finished task — it was
    // stopped, crashed, or the app was killed mid-task. Such a row gets a ⏸
    // marker, and ▶ asks whether to resume that conversation or start over
    // instead of silently resuming (see ResumePrompt, CLAUDE.md "Crash Recovery").
    type UnfinishedState = { session_id: string; started_at?: any; task?: string };
    let unfinished: Record<string, UnfinishedState> = {};
    let resumeFor: { project: string; name: string; state: UnfinishedState } | null = null;

    async function loadState(project: string, name: string): Promise<UnfinishedState | null> {
        try {
            const st = (await GetSessionState(project, name)) as UnfinishedState | null;
            return st && st.session_id ? st : null;
        } catch (err) {
            // A failed check must never block starting a session.
            console.warn('GetSessionState failed:', err);
            return null;
        }
    }

    function forgetState(id: string) {
        if (!unfinished[id]) return;
        const { [id]: _dropped, ...rest } = unfinished;
        unfinished = rest;
    }

    async function refreshState(s: { project: string; name: string; id: string }) {
        const st = await loadState(s.project, s.name);
        if (st) unfinished = { ...unfinished, [s.id]: st };
        else forgetState(s.id);
    }

    // Re-check the state file on every status change: it appears when a run is
    // interrupted and is deleted when a task completes cleanly, so the marker
    // stays honest without polling.
    let seenStatus: Record<string, SessionStatus> = {};
    $: {
        for (const g of $projectGroups) {
            for (const s of g.sessions) {
                syncQueue(s);
                if (seenStatus[s.id] === s.status) continue;
                seenStatus[s.id] = s.status;
                if (isRunning(s)) forgetState(s.id);
                else refreshState(s);
            }
        }
    }

    function statusColor(status: SessionStatus): string {
        switch (status) {
            case 'working': return 'bg-status-working';
            case 'waiting_permission':
            case 'waiting_for_user': return 'bg-status-waiting';
            case 'rate_limited':
            case 'retrying': return 'bg-status-ratelimit';
            case 'error': return 'bg-status-error';
            case 'starting':
            case 'analyzing': return 'bg-status-starting';
            case 'stopping': return 'bg-status-idle';
            case 'idle':
            default: return 'bg-status-idle';
        }
    }

    function statusLabel(s: SessionState, tr: (key: string, params?: Record<string, string | number>) => string): string {
        const suffix = s.stop_requested ? tr('sidebar.stoppingAfterTaskSuffix') : '';
        switch (s.status) {
            case 'working': return (s.current_task ? tr('sidebar.statusWorkingTask', { task: s.current_task }) : tr('sidebar.statusWorking')) + suffix;
            case 'waiting_permission': return tr('sidebar.statusWaitingPermission') + suffix;
            case 'waiting_for_user': return tr('sidebar.statusWaitingForAnswer') + suffix;
            case 'rate_limited': return tr('sidebar.statusRateLimited') + suffix;
            case 'retrying': return tr('sidebar.statusRetrying') + suffix;
            case 'error': return tr('sidebar.statusError');
            case 'starting': return tr('sidebar.statusStarting');
            case 'analyzing': return tr('sidebar.statusAnalyzing') + suffix;
            case 'stopping': return tr('sidebar.statusStopping');
            case 'idle':
            default: return tr('sidebar.statusIdle');
        }
    }

    function uptime(s: SessionState): string {
        if (!s.started_at) return '';
        const t = new Date(s.started_at).getTime();
        if (!t || isNaN(t)) return '';
        const ms = Date.now() - t;
        if (ms < 0) return '';
        const m = Math.floor(ms / 60000);
        if (m < 60) return `${m}m`;
        const h = Math.floor(m / 60);
        return `${h}h ${m % 60}m`;
    }

    function isRunning(s: SessionState): boolean {
        return s.status !== 'idle' && s.status !== 'error';
    }

    function select(id: string) {
        selectedSessionId.set(id);
    }

    // Start path shared by the plain ▶ click and by the resume prompt's
    // Continue / Start fresh buttons.
    async function beginStart(project: string, name: string, skipPreflight = false) {
        if (!skipPreflight) {
            const id = `${project}/${name}`;
            analyzing = { ...analyzing, [id]: true };
            let plan: any = null;
            try {
                plan = await SessionPreflight(project, name);
            } catch (err) {
                // The analysis is advisory: a failed analyst run must not
                // keep the session from starting.
                console.warn('pre-flight failed, starting without it:', err);
            } finally {
                analyzing = { ...analyzing, [id]: false };
            }
            if (plan) {
                planFor = { project, name, plan };
                return;
            }
        }
        // Read at click time, not once on mount: a change in Settings or the
        // Features popup applies to the very next start.
        if (await GetAutoModelRouting().catch(() => false)) {
            pickerFor = { project, name };
            return;
        }
        await StartSession(project, name);
    }

    async function onToggleSession(e: Event, s: SessionState) {
        e.stopPropagation();
        try {
            if (isRunning(s)) {
                await StopSession(s.id, true);
                return;
            }
            // Before starting: was the previous run of this session finished?
            // A leftover state file with a CLI session id says no, so ask
            // instead of silently resuming it (or silently discarding it).
            const st = await loadState(s.project, s.name);
            if (st) {
                resumeFor = { project: s.project, name: s.name, state: st };
                return;
            }
            await beginStart(s.project, s.name);
        } catch (err) {
            console.warn('toggle session failed:', err);
        }
    }

    async function onResumeChoice(fresh: boolean) {
        const r = resumeFor;
        resumeFor = null;
        if (!r) return;
        try {
            if (fresh) {
                // Equivalent of the orchestrator's --new: drop the saved
                // conversation so the next launch uses --session-id, not --resume.
                await ClearSessionState(r.project, r.name);
                forgetState(`${r.project}/${r.name}`);
            }
            await beginStart(r.project, r.name);
        } catch (err) {
            console.warn('start after resume choice failed:', err);
        }
    }

    async function onStartProject(e: Event, name: string) {
        e.stopPropagation();
        try {
            await StartProject(name);
        } catch (err) {
            console.warn('start project failed:', err);
        }
    }

    async function onStopProject(e: Event, name: string) {
        e.stopPropagation();
        try {
            await StopProject(name);
        } catch (err) {
            console.warn('stop project failed:', err);
        }
    }

    function isBlinking(s: SessionStatus): boolean {
        return s === 'waiting_permission' || s === 'waiting_for_user';
    }

    let pendingDelete: string | null = null;
    let pendingDeleteTimer: ReturnType<typeof setTimeout> | null = null;

    async function onDeleteProject(e: Event, name: string) {
        e.stopPropagation();
        if (pendingDelete !== name) {
            // First click — arm the button, auto-cancel after 3 seconds
            pendingDelete = name;
            if (pendingDeleteTimer) clearTimeout(pendingDeleteTimer);
            pendingDeleteTimer = setTimeout(() => { pendingDelete = null; }, 3000);
            return;
        }
        // Second click — confirmed
        pendingDelete = null;
        if (pendingDeleteTimer) clearTimeout(pendingDeleteTimer);
        try {
            const raw = await GetConfig();
            raw.Projects = (raw.Projects ?? []).filter((p: any) => p.Name !== name);
            await UpdateConfig(raw as any);
            await initProjects();
        } catch (err) {
            console.warn('delete project failed:', err);
        }
    }
</script>

<aside class="w-full h-full bg-bg-panel flex flex-col">
    <div class="px-3 py-2 border-b border-bg-border flex items-center justify-between">
        <span class="text-text font-semibold text-sm">{$t('sidebar.projectsHeading')}</span>
        <button
            class="text-text-muted hover:text-text text-xs px-1.5 py-0.5 rounded hover:bg-bg-elevated"
            title={$t('sidebar.addProjectTooltip')}
            on:click={() => dispatch('openAddProject')}
            type="button">+</button>
    </div>

    <div class="flex-1 overflow-y-auto py-1">
        {#each $projectGroups as group (group.name)}
            {@const collapsed = $collapsedProjects[group.name] ?? false}
            <div class="mb-1">
                <div
                    class="group flex items-center px-2 py-1 hover:bg-bg-elevated cursor-pointer select-none"
                    on:click={() => onToggleProjectHeader(group)}
                    on:keydown={(e) => e.key === 'Enter' && onToggleProjectHeader(group)}
                    role="button"
                    tabindex="0">
                    <span class="text-text-muted w-3 text-center">{collapsed ? '▶' : '▼'}</span>
                    <span class="text-text font-medium ml-1 flex-1 truncate" style="font-size: 17px" title={group.path}>{group.name}</span>
                    <button
                        class="opacity-0 group-hover:opacity-100 text-text-muted hover:text-status-working px-1 text-xs"
                        title={$t('sidebar.startAllTooltip')}
                        on:click={(e) => onStartProject(e, group.name)}
                        type="button">▶</button>
                    <button
                        class="opacity-0 group-hover:opacity-100 text-text-muted hover:text-status-error px-1 text-xs"
                        title={$t('sidebar.stopAllTooltip')}
                        on:click={(e) => onStopProject(e, group.name)}
                        type="button">■</button>
                    <button
                        class="px-1 text-xs
                            {pendingDelete === group.name
                                ? 'opacity-100 text-status-error font-bold'
                                : 'opacity-0 group-hover:opacity-100 text-text-muted hover:text-status-error'}"
                        title={pendingDelete === group.name ? $t('sidebar.confirmDeleteTooltip') : $t('sidebar.deleteProjectTooltip')}
                        on:click={(e) => onDeleteProject(e, group.name)}
                        type="button">{pendingDelete === group.name ? '?' : '✕'}</button>
                </div>

                {#if claudeMdStatus[group.name] === false && !dismissedClaudeMd.has(group.name)}
                    <div class="mx-2 mb-1 px-2 py-1.5 rounded bg-bg-elevated border border-bg-border flex items-center gap-2">
                        <span class="text-text-muted text-xs flex-1">{$t('sidebar.noClaudeMd')}</span>
                        <button
                            class="text-status-working hover:underline text-xs whitespace-nowrap"
                            on:click={(e) => onGenerateClaudeMd(e, group.name)}
                            disabled={claudeMdBusy[group.name]}
                            type="button">{claudeMdBusy[group.name] ? $t('sidebar.generating') : $t('sidebar.generate')}</button>
                        <button
                            class="text-text-dim hover:text-text text-xs"
                            title={$t('sidebar.dismissTooltip')}
                            on:click={(e) => onDismissClaudeMd(e, group.name)}
                            type="button">✕</button>
                    </div>
                {/if}

                {#if !collapsed}
                    {#each group.sessions as s (s.id)}
                        {@const selected = $selectedSessionId === s.id}
                        <div
                            class="group pl-6 pr-2 py-1 cursor-pointer
                                {selected ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/60'}"
                            on:click={() => select(s.id)}
                            on:keydown={(e) => e.key === 'Enter' && select(s.id)}
                            role="button"
                            tabindex="0">
                            <div class="flex items-center">
                                <span
                                    class="w-2.5 h-2.5 rounded-full mr-2 shrink-0 {statusColor(s.status)} {isBlinking(s.status) ? 'dot-blink' : ''}
                                           {s.stop_requested ? 'ring-2 ring-amber-400' : ''}"
                                    title={statusLabel(s, $t)}></span>
                                <button
                                    type="button"
                                    class="mr-1.5 shrink-0 leading-none text-text-muted hover:text-text"
                                    style="font-size: 13px"
                                    data-testid="session-features-button-{s.id}"
                                    title={$t('sessionFeatures.buttonTitle')}
                                    aria-label={$t('sessionFeatures.button')}
                                    on:click|stopPropagation={() => (featuresFor = { project: s.project, name: s.name })}
                                    on:keydown|stopPropagation>⚙</button>
                                <span class="text-text truncate flex-1" style="font-size: 13px">{s.name}</span>
                                {#if analyzing[s.id]}
                                    <span class="mr-1 shrink-0" style="font-size: 11px"
                                          data-testid="preflight-busy-{s.id}"
                                          title={$t('sidebar.preflightRunning')}>🔎</span>
                                {/if}
                                {#if s.status === 'idle' && s.stopped_after_task_at}
                                    <span
                                        class="text-amber-400 mr-1 shrink-0"
                                        style="font-size: 11px"
                                        data-testid="stopped-after-task-{s.id}"
                                        title={$t('sidebar.stoppedAfterTaskTooltip', { time: new Date(s.stopped_after_task_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) })}>⏹</span>
                                {/if}
                                {#if unfinished[s.id]}
                                    <span
                                        class="text-amber-400 mr-1 shrink-0"
                                        style="font-size: 11px"
                                        data-testid="unfinished-{s.id}"
                                        title={unfinished[s.id].task
                                            ? $t('sidebar.unfinishedTooltipTask', { task: unfinished[s.id].task })
                                            : $t('sidebar.unfinishedTooltip')}>⏸</span>
                                {/if}
                                <!-- A disabled <select> swallows clicks in Chromium, so while
                                     it's locked it lets them through to the row
                                     (pointer-events-none) and the wrapper keeps the tooltip. -->
                                <span
                                    class="ml-1.5 shrink-0 inline-flex"
                                    title={isRunning(s) ? $t('sidebar.runtimeLockedTooltip') : $t('sidebar.runtimeTooltip')}>
                                <select
                                    value={aiValueOf(s)}
                                    disabled={!!modelBusy[s.id] || isRunning(s)}
                                    on:focus={() => select(s.id)}
                                    on:change={(e) => onAIChange(e, s)}
                                    data-testid="runtime-select-{s.id}"
                                    class="bg-bg border border-bg-border rounded px-1 disabled:opacity-60 disabled:pointer-events-none
                                           {providerOfSession(s) ? 'text-blue-400' : s.runtime === 'hermes' ? 'text-amber-400' : 'text-text-muted'}"
                                    style="font-size: 10px; line-height: 1.4;">
                                    <option value="claude">Claude</option>
                                    <option value="hermes">☤ Hermes</option>
                                    {#each providers as p}
                                        <option value={'prov:' + p.id}>{p.label}</option>
                                    {/each}
                                </select>
                                </span>
                                <select
                                    value={providerOfSession(s) ? comboValueOf(s) : modelValue(s)}
                                    disabled={!!modelBusy[s.id]}
                                    on:focus={() => select(s.id)}
                                    on:change={(e) => (providerOfSession(s) || isPlainHermes(s)
                                        ? onComboChange(e, s)
                                        : onModelChange(e, s))}
                                    data-testid="model-select-{s.id}"
                                    title={s.runtime === 'hermes'
                                        ? $t('sidebar.hermesTooltip', { model: s.model || '—' })
                                        : $t('sidebar.modelTooltip', { model: s.model || '—' })}
                                    class="ml-1 shrink-0 bg-bg border border-bg-border rounded px-1 text-text-muted max-w-[11rem]
                                           disabled:opacity-50 disabled:pointer-events-none"
                                    style="font-size: 10px; line-height: 1.4;">
                                    {#if providerOfSession(s) || isPlainHermes(s)}
                                        <!-- A provider brain (or a plain hermes session, whose model
                                             list is the hermes-capable providers'): one flat model×effort list -->
                                        {#each providerCombosFor(s) as c}
                                            <option value={c.value}>{c.label}</option>
                                        {/each}
                                        {#if isPlainHermes(s) && !comboMatchesModel(s)}
                                            <!-- The pinned model id (e.g. a leftover CLI alias) is not
                                                 among the combos — show it as its own option rather
                                                 than silently selecting a different model. -->
                                            <option value="custom">{s.model}</option>
                                        {/if}
                                    {:else}
                                        {#if !s.model}
                                            <option value="" disabled>—</option>
                                        {/if}
                                        {#each MODELS as m}
                                            <option value={m.value}>{m.label}</option>
                                        {/each}
                                        {#if hasCustomModel(s)}
                                            <!-- A custom/pinned id from config that isn't one of ours -->
                                            <option value={modelValue(s)}>{s.model}</option>
                                        {/if}
                                    {/if}
                                </select>
                                {#if modelBusy[s.id]}
                                    <span class="text-text-dim ml-1" style="font-size: 10px">…</span>
                                {/if}
                                {#if uptime(s)}
                                    <span class="text-text-dim text-xs ml-2 hidden group-hover:hidden">{uptime(s)}</span>
                                {/if}
                                <button
                                    class="px-1 text-xs shrink-0
                                           {s.stop_requested
                                               ? 'opacity-100 text-amber-400'
                                               : 'opacity-0 group-hover:opacity-100 text-text-muted hover:text-text'}"
                                    title={s.stop_requested
                                        ? $t('sidebar.stopRequestedTooltip')
                                        : isRunning(s) ? $t('sidebar.stopTooltip') : $t('sidebar.startTooltip')}
                                    on:click={(e) => onToggleSession(e, s)}
                                    type="button">
                                    {s.stop_requested ? '⏳' : isRunning(s) ? '■' : '▶'}
                                </button>
                            </div>
                            <div class="pl-4">
                                <QueueMiniBar session={s} running={isRunning(s)} />
                            </div>
                            {#if modelError[s.id]}
                                <div class="text-status-error pl-4" style="font-size: 10px">{modelError[s.id]}</div>
                            {/if}
                        </div>
                    {/each}
                {/if}
            </div>
        {/each}

        {#if $projectGroups.length === 0}
            <div class="px-3 py-4 text-center text-text-muted text-xs">
                {$t('sidebar.noProjects')}
            </div>
        {/if}
    </div>

</aside>

{#if pickerFor}
    <ModelPicker
        project={pickerFor.project}
        sessionName={pickerFor.name}
        on:confirm={async (e) => {
            const p = pickerFor;
            pickerFor = null;
            if (!p) return;
            const d = e.detail;
            try {
                if (d.provider) {
                    await StartSessionWithProviderAndModel(p.project, p.name, d.provider, d.model, d.effort);
                } else {
                    await StartSessionWithModel(p.project, p.name, d.model, d.effort);
                }
            } catch (err) { console.warn('start with model failed:', err); }
        }}
        on:cancel={() => { pickerFor = null; }}
    />
{/if}

{#if planFor}
    <PlanReview
        plan={planFor.plan}
        startSessionLabel={$t('sidebar.preflightStartAnyway')}
        on:startSession={() => {
            const p = planFor;
            planFor = null;
            if (p) beginStart(p.project, p.name, true).catch((err) => console.warn('start failed:', err));
        }}
        on:executed={() => { planFor = null; }}
        on:cancel={() => { planFor = null; }}
    />
{/if}

{#if featuresFor}
    <SessionFeatures
        project={featuresFor.project}
        sessionName={featuresFor.name}
        on:close={() => { featuresFor = null; }}
    />
{/if}

{#if resumeFor}
    <ResumePrompt
        project={resumeFor.project}
        sessionName={resumeFor.name}
        state={resumeFor.state}
        on:continue={() => onResumeChoice(false)}
        on:fresh={() => onResumeChoice(true)}
        on:cancel={() => { resumeFor = null; }}
    />
{/if}
