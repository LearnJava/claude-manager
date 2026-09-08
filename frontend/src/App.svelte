<script lang="ts">
    import { onMount } from 'svelte';
    import Sidebar from './components/Sidebar.svelte';
    import StatusBar from './components/StatusBar.svelte';
    import SessionView from './components/SessionView.svelte';
    import PermissionQueue from './components/PermissionQueue.svelte';
    import Settings from './components/Settings.svelte';
    import History from './components/History.svelte';
    import CostDashboard from './components/CostDashboard.svelte';
    import MixedRun from './components/MixedRun.svelte';
    import ExperiencePanel from './components/ExperiencePanel.svelte';
    import { initSessions, selectedSessionId, sessions, sessionList, waitingSessions } from './stores/sessions';
    import { initProjects } from './stores/projects';
    // Importing the store has the side effect of subscribing to localStorage
    // and applying the persisted theme to <html> on app boot.
    import './stores/theme';
    import { logSearch } from './stores/logSearch';

    let showPermissionQueue = false;
    let showSettings = false;
    let settingsInitialTab: 'global' | 'projects' | 'sessions' = 'global';
    let settingsInitialAction: 'add' | undefined = undefined;
    let showHistory = false;
    let showDashboard = false;
    let showMixedRun = false;
    let showExperience = false;

    // ---- Resizable sidebar ----
    let sidebarWidth = 250;
    let resizing = false;

    function onDividerMouseDown(e: MouseEvent) {
        resizing = true;
        e.preventDefault();
    }

    function onWindowMouseMove(e: MouseEvent) {
        if (!resizing) return;
        sidebarWidth = Math.max(150, Math.min(500, e.clientX));
    }

    function onWindowMouseUp() {
        resizing = false;
    }

    onMount(async () => {
        // The theme store subscribes during import and applies the `dark`
        // class to <html> — no manual classList work needed here.
        await Promise.all([initProjects(), initSessions()]);
    });

    // ---- Global keyboard shortcuts (PLAN.md section 10 stage 8) ----
    // Ctrl+1..4 — switch between the first four sessions in the sidebar order.
    // Ctrl+F    — focus the log search box (LogStream listens for this).
    function onKeyDown(e: KeyboardEvent) {
        if (!e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) return;
        const k = e.key;
        if (k === 'f' || k === 'F') {
            // Don't hijack the in-page Find when typing in an input/textarea —
            // unless the user is in the log itself, where Ctrl+F should jump
            // to our filter box.
            const tag = (document.activeElement?.tagName ?? '').toLowerCase();
            if (tag === 'input' || tag === 'textarea') return;
            e.preventDefault();
            logSearch.focus();
            return;
        }
        if (k >= '1' && k <= '4') {
            const idx = parseInt(k, 10) - 1;
            const list = $sessionList;
            if (idx < list.length) {
                e.preventDefault();
                selectedSessionId.set(list[idx].id);
            }
        }
    }

    $: selected = $selectedSessionId ? $sessions[$selectedSessionId] : null;

    function openQueue() {
        showPermissionQueue = true;
    }
    function closeQueue() {
        showPermissionQueue = false;
    }

    function openSettings() {
        settingsInitialTab = 'global';
        settingsInitialAction = undefined;
        showSettings = true;
    }
    function openAddProject() {
        settingsInitialTab = 'projects';
        settingsInitialAction = 'add';
        showSettings = true;
    }
    function closeSettings() {
        showSettings = false;
    }
</script>

<svelte:window on:keydown={onKeyDown} on:mousemove={onWindowMouseMove} on:mouseup={onWindowMouseUp} />

<div class="h-screen w-screen flex flex-col bg-bg text-text">
    <!-- Title bar -->
    <header class="h-9 bg-bg-panel border-b border-bg-border px-3 flex items-center justify-between select-none">
        <span class="font-semibold text-text">Claude Session Manager</span>
        <div class="flex items-center gap-2">
            <button
                class="text-text-muted hover:text-text text-xs px-2 py-0.5 rounded hover:bg-bg-elevated"
                on:click={() => (showExperience = true)}
                type="button">Experience</button>
            <button
                class="text-text-muted hover:text-text text-xs px-2 py-0.5 rounded hover:bg-bg-elevated"
                on:click={() => (showMixedRun = true)}
                type="button">Mixed</button>
            <button
                class="text-text-muted hover:text-text text-xs px-2 py-0.5 rounded hover:bg-bg-elevated"
                on:click={() => (showDashboard = true)}
                type="button">Dashboard</button>
            <button
                class="text-text-muted hover:text-text text-xs px-2 py-0.5 rounded hover:bg-bg-elevated"
                on:click={() => (showHistory = true)}
                type="button">History</button>
            <button
                class="text-text-muted hover:text-text text-xs px-2 py-0.5 rounded hover:bg-bg-elevated"
                on:click={openSettings}
                type="button">Settings</button>
        </div>
    </header>

    <!-- Main row: sidebar + resizer + content -->
    <div class="flex-1 flex min-h-0" class:cursor-col-resize={resizing}>
        <div style="width: {sidebarWidth}px; flex-shrink: 0; height: 100%;">
            <Sidebar on:openSettings={openSettings} on:openAddProject={openAddProject} />
        </div>

        <!-- Drag handle -->
        <div
            class="w-1 flex-shrink-0 bg-bg-border hover:bg-blue-500 cursor-col-resize transition-colors"
            class:bg-blue-500={resizing}
            on:mousedown={onDividerMouseDown}
            role="separator"
            aria-orientation="vertical"
            aria-label="Resize sidebar"
            tabindex="-1"
        ></div>

        <main class="flex-1 min-w-0 flex flex-col overflow-hidden">
            {#if selected}
                <SessionView session={selected} />
            {:else}
                <div class="flex-1 flex items-center justify-center text-text-muted text-sm">
                    Select a session from the sidebar.
                </div>
            {/if}
        </main>
    </div>

    <StatusBar on:openQueue={openQueue} />

    {#if showPermissionQueue}
        <div
            class="absolute inset-0 bg-black/40 flex items-center justify-center z-50"
            on:click={closeQueue}
            on:keydown={(e) => e.key === 'Escape' && closeQueue()}
            role="dialog"
            tabindex="-1">
            <div
                class="bg-bg-panel border border-bg-border rounded p-4 min-w-[520px] max-w-[80%] max-h-[80%] flex flex-col"
                role="document"
                on:click|stopPropagation
                on:keydown|stopPropagation>
                <div class="flex items-center justify-between mb-3 shrink-0">
                    <h3 class="text-text font-semibold">
                        Permission Queue
                        <span class="text-text-muted text-xs font-normal ml-1">
                            ({$waitingSessions.length} pending)
                        </span>
                    </h3>
                    <button
                        class="text-text-muted hover:text-text text-sm px-2 py-0.5"
                        on:click={closeQueue}
                        type="button">✕</button>
                </div>
                <PermissionQueue />
            </div>
        </div>
    {/if}

    {#if showSettings}
        <Settings initialTab={settingsInitialTab} initialAction={settingsInitialAction} on:close={closeSettings} />
    {/if}

    {#if showHistory}
        <History on:close={() => (showHistory = false)} />
    {/if}

    {#if showDashboard}
        <CostDashboard on:close={() => (showDashboard = false)} />
    {/if}

    {#if showMixedRun}
        <MixedRun on:close={() => (showMixedRun = false)} />
    {/if}

    {#if showExperience}
        <ExperiencePanel on:close={() => (showExperience = false)} />
    {/if}
</div>

