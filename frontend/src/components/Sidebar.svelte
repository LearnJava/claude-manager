<script lang="ts">
    import { createEventDispatcher, onMount } from 'svelte';
    import { projectGroups, collapsedProjects, toggleProject, initProjects } from '../stores/projects';
    import ModelPicker from './ModelPicker.svelte';
    import AlienCrew from './AlienCrew.svelte';

    const dispatch = createEventDispatcher();
    import { selectedSessionId, type SessionState, type SessionStatus } from '../stores/sessions';
    import {
        StartSession,
        StopSession,
        StartProject,
        StopProject,
        GetConfig,
        UpdateConfig,
        GetAutoModelRouting,
        StartSessionWithModel,
    } from '../../wailsjs/go/main/App';

    let autoModelRouting = false;
    onMount(async () => {
        try { autoModelRouting = await GetAutoModelRouting(); } catch {}
    });

    // Session for which the ModelPicker is currently open: "project/name" or null
    let pickerFor: { project: string; name: string } | null = null;

    function statusColor(status: SessionStatus): string {
        switch (status) {
            case 'working': return 'bg-status-working';
            case 'waiting_permission': return 'bg-status-waiting';
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
        switch (s.status) {
            case 'working': return s.current_task ? `Working — ${s.current_task}` : 'Working';
            case 'waiting_permission': return 'Waiting permission';
            case 'rate_limited': return 'Rate limited';
            case 'retrying': return 'Retrying';
            case 'error': return 'Error';
            case 'starting': return 'Starting';
            case 'analyzing': return 'Analyzing';
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

    async function onToggleSession(e: Event, s: SessionState) {
        e.stopPropagation();
        try {
            if (isRunning(s)) {
                await StopSession(s.id, true);
            } else if (autoModelRouting) {
                pickerFor = { project: s.project, name: s.name };
            } else {
                await StartSession(s.project, s.name);
            }
        } catch (err) {
            console.warn('toggle session failed:', err);
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
        return s === 'waiting_permission';
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
                    on:click={() => toggleProject(group.name)}
                    on:keydown={(e) => e.key === 'Enter' && toggleProject(group.name)}
                    role="button"
                    tabindex="0">
                    <span class="text-text-muted w-3 text-center">{collapsed ? '▶' : '▼'}</span>
                    <span class="text-text font-medium ml-1 flex-1 truncate" style="font-size: 17px" title={group.path}>{group.name}</span>
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

                {#if !collapsed}
                    {#each group.sessions as s (s.id)}
                        {@const selected = $selectedSessionId === s.id}
                        <div
                            class="group flex items-center pl-6 pr-2 py-1 cursor-pointer
                                {selected ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/60'}"
                            on:click={() => select(s.id)}
                            on:keydown={(e) => e.key === 'Enter' && select(s.id)}
                            role="button"
                            tabindex="0">
                            <span
                                class="w-2.5 h-2.5 rounded-full mr-2 {statusColor(s.status)} {isBlinking(s.status) ? 'dot-blink' : ''}"
                                title={statusLabel(s)}></span>
                            <span class="text-text truncate flex-1" style="font-size: 13px">{s.name}</span>
                            {#if uptime(s)}
                                <span class="text-text-dim text-xs ml-2 hidden group-hover:hidden">{uptime(s)}</span>
                            {/if}
                            <button
                                class="opacity-0 group-hover:opacity-100 text-text-muted hover:text-text px-1 text-xs"
                                title={isRunning(s) ? 'Stop' : 'Start'}
                                on:click={(e) => onToggleSession(e, s)}
                                type="button">
                                {isRunning(s) ? '■' : '▶'}
                            </button>
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
