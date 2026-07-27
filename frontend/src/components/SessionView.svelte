<script lang="ts">
    import SessionCard from './SessionCard.svelte';
    import LogStream from './LogStream.svelte';
    import TaskPanel from './TaskPanel.svelte';
    import SessionInput from './SessionInput.svelte';
    import PermissionBanner from './PermissionBanner.svelte';
    import QuestionBanner from './QuestionBanner.svelte';
    import {
        clearSessionLog,
        sessionLogs,
        type SessionState,
    } from '../stores/sessions';
    import { projects } from '../stores/projects';
    import { formatTime } from '../lib/formatters';
    import {
        StopSession,
        RestartSession,
        ExportLog,
        InitGitRepo,
    } from '../../wailsjs/go/main/App';

    export let session: SessionState;

    let busy: string | null = null;
    let lastError = '';
    let copyConfirm = false;
    let exportFormat: 'md' | 'json' | 'txt' = 'md';
    let exportConfirm = '';

    function busyMatches(label: string): boolean {
        return busy === label;
    }

    async function call(label: string, fn: () => Promise<unknown>) {
        if (busy) return;
        busy = label;
        lastError = '';
        try {
            await fn();
        } catch (e: any) {
            lastError = `${label}: ${e?.message ?? String(e)}`;
        } finally {
            busy = null;
        }
    }

    // The Go layer currently exposes one soft-stop. "Pause" and
    // "Stop after task" both ask the session to finish the current task and
    // not start a new one; a dedicated Pause binding can refine this later.
    function onPause() {
        call('Pause', () => StopSession(session.id, true));
    }

    function onStop() {
        call('Stop', () => StopSession(session.id, false));
    }

    function onStopAfterTask() {
        call('Stop after task', () => StopSession(session.id, true));
    }

    function onRestart() {
        call('Restart', () => RestartSession(session.id));
    }

    async function onCopyLog() {
        const entries = $sessionLogs[session.id] ?? [];
        const text = entries
            .map((e) => `[${formatTime(e.time)}] ${e.level ?? ''} ${e.message ?? ''}`)
            .join('\n');
        try {
            await navigator.clipboard.writeText(text);
            copyConfirm = true;
            setTimeout(() => (copyConfirm = false), 1500);
        } catch (e: any) {
            lastError = `Copy log: ${e?.message ?? String(e)}`;
        }
    }

    function onClearLog() {
        clearSessionLog(session.id);
    }

    // `--worktree` needs the project folder to be a git repo with at least
    // one commit — a bare `git init` leaves an unborn HEAD it can't branch
    // from. Detect the CLI's own error text in the log and offer a one-click
    // fix instead of a dead-end red line.
    const GIT_WORKTREE_ERROR_MARKERS = [
        'is not a git repository',
        'Failed to resolve base branch',
    ];
    let gitInitBusy = false;
    let gitInitError = '';

    $: projectPath = $projects.find((p) => p.name === session.project)?.path ?? '';
    $: needsGitInit = ($sessionLogs[session.id] ?? [])
        .slice(-30)
        .some(
            (e) =>
                e.level === 'error' &&
                GIT_WORKTREE_ERROR_MARKERS.some((m) => (e.message ?? '').includes(m)),
        );

    async function onInitGitRepo() {
        if (gitInitBusy || !projectPath) return;
        gitInitBusy = true;
        gitInitError = '';
        try {
            await InitGitRepo(projectPath);
            clearSessionLog(session.id);
            await RestartSession(session.id);
        } catch (e: any) {
            gitInitError = `Init git repo failed: ${e?.message ?? String(e)}`;
        } finally {
            gitInitBusy = false;
        }
    }

    async function onExportLog() {
        const entries = $sessionLogs[session.id] ?? [];
        // Map the in-memory LogEntry shape to the Go store.LogEntry payload
        // so the backend can render markdown / JSON / txt identically.
        const payload = entries.map((e) => ({
            ID: 0,
            RunID: 0,
            Timestamp: e.time,
            Level: e.level ?? '',
            Message: e.message ?? '',
            ToolName: e.tool_name ?? '',
            ToolInput: e.tool_input ?? '',
        }));
        await call(`Export log (${exportFormat})`, async () => {
            const path = await ExportLog(session.id, payload as any, exportFormat);
            if (path) {
                exportConfirm = `Saved → ${path}`;
                setTimeout(() => (exportConfirm = ''), 3000);
            } else {
                exportConfirm = ''; // user cancelled
            }
        });
    }

    // Button enabled states — Stop/Pause require an active process,
    // Restart works whenever we know the session id.
    $: isRunning =
        session.status !== 'idle' &&
        session.status !== 'error' &&
        session.status !== 'stopping';
</script>

<div class="flex-1 min-h-0 flex flex-col">
    <!-- Header card -->
    <div class="px-3 pt-3">
        <SessionCard {session} />
    </div>

    <!-- Log stream + task panel fill remaining vertical space -->
    <div class="flex-1 min-h-0 mt-2 mx-3 border border-bg-border rounded overflow-hidden flex flex-row">
        <div class="flex-1 min-w-0 flex flex-col">
            {#if session.pending_permission}
                <PermissionBanner {session} />
            {/if}
            {#if session.pending_question}
                <QuestionBanner {session} />
            {/if}
            {#if needsGitInit}
                <div class="px-3 py-2 bg-status-error/10 border-b border-status-error/30
                            flex items-center justify-between gap-3 text-xs">
                    <span class="text-text">
                        This session needs <code class="font-mono">--worktree</code>, but
                        <code class="font-mono">{projectPath || session.project}</code>
                        isn't a usable git repository yet (not initialized, or has no
                        commits — a bare <code class="font-mono">git init</code> alone
                        isn't enough).
                    </span>
                    <button
                        type="button"
                        on:click={onInitGitRepo}
                        disabled={gitInitBusy || !projectPath}
                        class="px-2.5 py-1 rounded bg-blue-600 hover:bg-blue-500 text-white
                               disabled:opacity-50 disabled:cursor-not-allowed whitespace-nowrap">
                        {gitInitBusy ? 'Initializing…' : 'Initialize git repo & retry'}
                    </button>
                </div>
                {#if gitInitError}
                    <div class="px-3 py-1 text-xs text-status-error">{gitInitError}</div>
                {/if}
            {/if}
            <LogStream sessionId={session.id} />
        </div>
        <TaskPanel {session} />
    </div>

    <!-- Control bar -->
    <div class="px-3 py-2 flex flex-wrap items-center gap-2">
        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded border disabled:cursor-not-allowed
                   {session.stop_requested
                       ? 'bg-amber-500/20 border-amber-500/50 text-amber-400 disabled:opacity-100'
                       : 'bg-bg-elevated border-bg-border text-text hover:bg-bg-panel disabled:opacity-40'}"
            disabled={!isRunning || !!busy || session.stop_requested}
            on:click={onPause}
            title={session.stop_requested
                ? 'Already requested: will stop once the current task finishes'
                : "Pause: finish current task, don't start the next"}>
            {#if busyMatches('Pause')}…{:else if session.stop_requested}⏸ Stopping after task…{:else}⏸ Pause{/if}
        </button>
        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={!isRunning || !!busy}
            on:click={onStop}
            title="Stop: terminate the process now">
            {busyMatches('Stop') ? '…' : '⏹'} Stop
        </button>
        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded border disabled:cursor-not-allowed
                   {session.stop_requested
                       ? 'bg-amber-500/20 border-amber-500/50 text-amber-400 disabled:opacity-100'
                       : 'bg-bg-elevated border-bg-border text-text hover:bg-bg-panel disabled:opacity-40'}"
            disabled={!isRunning || !!busy || session.stop_requested}
            on:click={onStopAfterTask}
            title={session.stop_requested
                ? 'Already requested: will stop once the current task finishes'
                : 'Soft stop: let the current task finish, then stop'}>
            {#if busyMatches('Stop after task')}…{:else if session.stop_requested}⏹ Stopping after task…{:else}⏹ Stop after task{/if}
        </button>
        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={!!busy}
            on:click={onRestart}
            title="Stop and start the session again">
            {busyMatches('Restart') ? '…' : '🔄'} Restart
        </button>

        <span class="w-px h-4 bg-bg-border mx-1"></span>

        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel"
            on:click={onCopyLog}
            title="Copy the full visible log to clipboard">
            {copyConfirm ? '✓ Copied' : '📋 Copy log'}
        </button>
        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel"
            on:click={onClearLog}
            title="Clear the on-screen log buffer (history is preserved in storage)">
            🗑 Clear log
        </button>

        <span class="w-px h-4 bg-bg-border mx-1"></span>

        <select
            bind:value={exportFormat}
            title="Export format"
            class="bg-bg-elevated border border-bg-border rounded px-1.5 py-1 text-xs text-text">
            <option value="md">Markdown</option>
            <option value="json">JSON</option>
            <option value="txt">Text</option>
        </select>
        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel disabled:opacity-40"
            disabled={!!busy}
            on:click={onExportLog}
            title="Save the visible log to a file">
            {busyMatches(`Export log (${exportFormat})`) ? '…' : '💾'} Export log
        </button>
        {#if exportConfirm}
            <span class="text-status-working text-xs ml-1">{exportConfirm}</span>
        {/if}

        {#if lastError}
            <span class="text-status-error text-xs ml-2">{lastError}</span>
        {/if}
    </div>

    <SessionInput {session} />
</div>
