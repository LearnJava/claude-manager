<script lang="ts">
    import SessionCard from './SessionCard.svelte';
    import LogStream from './LogStream.svelte';
    import SessionInput from './SessionInput.svelte';
    import PermissionBanner from './PermissionBanner.svelte';
    import {
        clearSessionLog,
        sessionLogs,
        type SessionState,
    } from '../stores/sessions';
    import { formatTime } from '../lib/formatters';
    import {
        StopSession,
        RestartSession,
        ExportLog,
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

    <!-- Log stream fills remaining vertical space -->
    <div class="flex-1 min-h-0 mt-2 mx-3 border border-bg-border rounded overflow-hidden flex flex-col">
        {#if session.pending_permission}
            <PermissionBanner {session} />
        {/if}
        <LogStream sessionId={session.id} />
    </div>

    <!-- Control bar -->
    <div class="px-3 py-2 flex flex-wrap items-center gap-2">
        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={!isRunning || !!busy}
            on:click={onPause}
            title="Pause: finish current task, don't start the next">
            {busyMatches('Pause') ? '…' : '⏸'} Pause
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
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={!isRunning || !!busy}
            on:click={onStopAfterTask}
            title="Soft stop: let the current task finish, then stop">
            {busyMatches('Stop after task') ? '…' : '⏹'} Stop after task
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
