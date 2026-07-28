<script lang="ts">
    import { createEventDispatcher, onMount } from 'svelte';
    import { projectGroups, collapsedProjects, toggleProject, initProjects } from '../stores/projects';
    import ModelPicker from './ModelPicker.svelte';
    import ResumePrompt from './ResumePrompt.svelte';
    import AlienCrew from './AlienCrew.svelte';
    import { MODELS, normalizeModel, isKnownModel } from '../lib/models';

    const dispatch = createEventDispatcher();
    import { selectedSessionId, sessions, type SessionState, type SessionStatus } from '../stores/sessions';
    import {
        StartSession,
        StopSession,
        StartProject,
        StopProject,
        GetConfig,
        UpdateConfig,
        GetAutoModelRouting,
        StartSessionWithModel,
        SetSessionModel,
        HasClaudeMd,
        GenerateClaudeMdSession,
        StartAdHocChatSession,
        GetSessionState,
        ClearSessionState,
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

    async function onModelChange(e: Event, s: SessionState) {
        e.stopPropagation();
        const model = (e.target as HTMLSelectElement).value;
        if (!model || model === normalizeModel(s.model)) return;
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

    let autoModelRouting = false;
    onMount(async () => {
        try { autoModelRouting = await GetAutoModelRouting(); } catch {}
    });

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

    // "just chat" button on the project header: bootstraps (or reuses) a
    // plain interactive "Chat" session with no task_source/auto_restart, so
    // the user can hand Claude ad-hoc instructions without configuring a
    // session or a ROADMAP/task file first (see StartAdHocChatSession).
    let chatBusy: Record<string, boolean> = {};

    async function onStartChat(e: Event, name: string) {
        e.stopPropagation();
        chatBusy = { ...chatBusy, [name]: true };
        try {
            await StartAdHocChatSession(name);
        } catch (err: any) {
            // Already running isn't an error from the user's point of view —
            // the session is right there; just select it below either way.
            const msg = String(err?.message ?? err ?? '');
            if (!msg.includes('already running')) {
                console.warn('start chat session failed:', err);
            }
        } finally {
            chatBusy = { ...chatBusy, [name]: false };
            selectedSessionId.set(`${name}/Chat`);
        }
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

    function statusLabel(s: SessionState): string {
        const suffix = s.stop_requested ? ' · stopping after task' : '';
        switch (s.status) {
            case 'working': return (s.current_task ? `Working — ${s.current_task}` : 'Working') + suffix;
            case 'waiting_permission': return 'Waiting permission' + suffix;
            case 'waiting_for_user': return 'Waiting for answer' + suffix;
            case 'rate_limited': return 'Rate limited' + suffix;
            case 'retrying': return 'Retrying' + suffix;
            case 'error': return 'Error';
            case 'starting': return 'Starting';
            case 'analyzing': return 'Analyzing' + suffix;
            case 'stopping': return 'Stopping';
            case 'idle':
            default: return 'Idle';
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
    async function beginStart(project: string, name: string) {
        if (autoModelRouting) {
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
        <span class="text-text font-semibold text-sm">Projects</span>
        <button
            class="text-text-muted hover:text-text text-xs px-1.5 py-0.5 rounded hover:bg-bg-elevated"
            title="Add project"
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
                        title="Just chat: start a plain session with no tasks, give Claude ad-hoc instructions"
                        disabled={chatBusy[group.name]}
                        on:click={(e) => onStartChat(e, group.name)}
                        type="button">{chatBusy[group.name] ? '…' : '💬'}</button>
                    <button
                        class="opacity-0 group-hover:opacity-100 text-text-muted hover:text-status-working px-1 text-xs"
                        title="Start all"
                        on:click={(e) => onStartProject(e, group.name)}
                        type="button">▶</button>
                    <button
                        class="opacity-0 group-hover:opacity-100 text-text-muted hover:text-status-error px-1 text-xs"
                        title="Stop all"
                        on:click={(e) => onStopProject(e, group.name)}
                        type="button">■</button>
                    <button
                        class="px-1 text-xs
                            {pendingDelete === group.name
                                ? 'opacity-100 text-status-error font-bold'
                                : 'opacity-0 group-hover:opacity-100 text-text-muted hover:text-status-error'}"
                        title={pendingDelete === group.name ? 'Click again to confirm delete' : 'Delete project'}
                        on:click={(e) => onDeleteProject(e, group.name)}
                        type="button">{pendingDelete === group.name ? '?' : '✕'}</button>
                </div>

                {#if claudeMdStatus[group.name] === false && !dismissedClaudeMd.has(group.name)}
                    <div class="mx-2 mb-1 px-2 py-1.5 rounded bg-bg-elevated border border-bg-border flex items-center gap-2">
                        <span class="text-text-muted text-xs flex-1">No CLAUDE.md in this project — sessions start with zero context.</span>
                        <button
                            class="text-status-working hover:underline text-xs whitespace-nowrap"
                            on:click={(e) => onGenerateClaudeMd(e, group.name)}
                            disabled={claudeMdBusy[group.name]}
                            type="button">{claudeMdBusy[group.name] ? 'Starting…' : 'Generate'}</button>
                        <button
                            class="text-text-dim hover:text-text text-xs"
                            title="Dismiss"
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
                                    title={statusLabel(s)}></span>
                                <span class="text-text truncate flex-1" style="font-size: 13px">{s.name}</span>
                                {#if unfinished[s.id]}
                                    <span
                                        class="text-amber-400 mr-1 shrink-0"
                                        style="font-size: 11px"
                                        data-testid="unfinished-{s.id}"
                                        title={`Previous run unfinished (interrupted or stopped)${unfinished[s.id].task ? ' — ' + unfinished[s.id].task : ''}. Click ▶ to continue it or begin from scratch.`}>⏸</span>
                                {/if}
                                <select
                                    value={normalizeModel(s.model)}
                                    disabled={!!modelBusy[s.id]}
                                    on:change={(e) => onModelChange(e, s)}
                                    title={`Model: ${s.model || '—'}. Switching applies on the next task for autonomous sessions; interactive ones restart now (same conversation). The choice is remembered as this session's default.`}
                                    class="ml-1.5 shrink-0 bg-bg border border-bg-border rounded px-1 text-text-muted
                                           disabled:opacity-50"
                                    style="font-size: 10px; line-height: 1.4;">
                                    {#each MODELS as m}
                                        <option value={m.value}>{m.label}</option>
                                    {/each}
                                    {#if s.model && !isKnownModel(s.model)}
                                        <!-- A custom/pinned id from config that isn't one of ours -->
                                        <option value={normalizeModel(s.model)}>{s.model}</option>
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
                                        ? 'Stop requested — will stop once the current task finishes'
                                        : isRunning(s) ? 'Stop (finishes current task first)' : 'Start'}
                                    on:click={(e) => onToggleSession(e, s)}
                                    type="button">
                                    {s.stop_requested ? '⏳' : isRunning(s) ? '■' : '▶'}
                                </button>
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
                No projects configured.
            </div>
        {/if}
    </div>

    <!-- Comic-book alien crew panel -->
    <AlienCrew />
</aside>

{#if pickerFor}
    <ModelPicker
        project={pickerFor.project}
        sessionName={pickerFor.name}
        on:confirm={async (e) => {
            const p = pickerFor;
            pickerFor = null;
            if (!p) return;
            try { await StartSessionWithModel(p.project, p.name, e.detail.model, e.detail.effort); }
            catch (err) { console.warn('start with model failed:', err); }
        }}
        on:cancel={() => { pickerFor = null; }}
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
