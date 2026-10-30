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
    import QueueBoard from './QueueBoard.svelte';
    import { queues, syncQueue } from '../stores/queues';
    import { EMPTY_QUEUE, hasQueue } from '../lib/queue';
    import { t } from '../lib/i18n';
    import { canInterrupt } from '../lib/interrupt';
    import {
        StopSession,
        InterruptSession,
        RestartSession,
        RestartSessionMode,
        InitGitRepo,
    } from '../../wailsjs/go/main/App';

    export let session: SessionState;

    let busy: string | null = null;
    let lastError = '';

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

    function onStop() {
        call('Stop', () => StopSession(session.id, false));
    }

    function onInterrupt() {
        call('Interrupt', () => InterruptSession(session.id));
    }

    function onStopAfterTask() {
        call('Stop after task', () => StopSession(session.id, true));
    }

    let restartDialog = false;

    function onRestart() {
        restartDialog = true;
    }

    function doRestart(fresh: boolean) {
        restartDialog = false;
        call('Restart', () => RestartSessionMode(session.id, fresh));
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
            gitInitError = `${$t('sessionView.gitInit.failedPrefix')}: ${e?.message ?? String(e)}`;
        } finally {
            gitInitBusy = false;
        }
    }

    // Log | Board: the board replaces the log stream and is only offered when
    // the session has a queue; the log stays the default for every session.
    let viewMode: 'log' | 'board' = 'log';
    let viewForSession = '';
    $: if (session.id !== viewForSession) {
        viewForSession = session.id;
        viewMode = 'log';
    }
    $: syncQueue(session);
    $: queueModel = $queues[session.id]?.model ?? EMPTY_QUEUE;
    $: boardAvailable = hasQueue(queueModel);
    $: showBoard = boardAvailable && viewMode === 'board';

    // Button enabled states — Stop/Stop after task require an active process,
    // Restart works whenever we know the session id.
    $: isRunning =
        session.status !== 'idle' &&
        session.status !== 'error' &&
        session.status !== 'stopping';
    $: interruptible = canInterrupt(session);
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
                        {$t('sessionView.gitInitBanner.needsPrefix')} <code class="font-mono">--worktree</code>{$t('sessionView.gitInitBanner.butSuffix')}
                        <code class="font-mono">{projectPath || session.project}</code>
                        {$t('sessionView.gitInitBanner.notUsable')} <code class="font-mono">git init</code> {$t('sessionView.gitInitBanner.aloneNotEnough')}
                    </span>
                    <button
                        type="button"
                        on:click={onInitGitRepo}
                        disabled={gitInitBusy || !projectPath}
                        class="px-2.5 py-1 rounded bg-blue-600 hover:bg-blue-500 text-white
                               disabled:opacity-50 disabled:cursor-not-allowed whitespace-nowrap">
                        {gitInitBusy ? $t('sessionView.gitInit.initializing') : $t('sessionView.gitInit.button')}
                    </button>
                </div>
                {#if gitInitError}
                    <div class="px-3 py-1 text-xs text-status-error">{gitInitError}</div>
                {/if}
            {/if}
            {#if boardAvailable}
                <div class="px-3 py-1 border-b border-bg-border flex items-center gap-1" data-testid="view-toggle">
                    {#each ['log', 'board'] as m}
                        <button
                            type="button"
                            data-testid="view-{m}"
                            on:click={() => (viewMode = m === 'board' ? 'board' : 'log')}
                            class="px-1.5 py-0.5 text-xs uppercase tracking-wide rounded
                                   {(showBoard ? 'board' : 'log') === m
                                ? 'text-text font-semibold bg-bg-elevated'
                                : 'text-text-muted hover:text-text'}">
                            {$t(m === 'board' ? 'queue.view.board' : 'queue.view.log')}
                        </button>
                    {/each}
                </div>
            {/if}
            {#if showBoard}
                <QueueBoard {session} />
            {:else}
                <LogStream sessionId={session.id} />
            {/if}
        </div>
        <TaskPanel {session} />
    </div>

    <!-- Control bar -->
    <div class="px-3 py-2 flex flex-wrap items-center gap-2">
        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={!isRunning || !!busy}
            on:click={onStop}
            title={$t('sessionView.stop.title')}>
            {busyMatches('Stop') ? '…' : '⏹'} {$t('sessionView.stop.label')}
        </button>
        <button
            type="button"
            data-testid="interrupt-button"
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={!interruptible || !!busy}
            on:click={onInterrupt}
            title={$t('sessionView.interrupt.title')}>
            {busyMatches('Interrupt') ? '…' : '■'} {$t('sessionView.interrupt.label')}
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
                ? $t('sessionView.stopAfterTask.titleRequested')
                : $t('sessionView.stopAfterTask.titleDefault')}>
            {#if busyMatches('Stop after task')}…{:else if session.stop_requested}{$t('sessionView.stopAfterTask.stopping')}{:else}{$t('sessionView.stopAfterTask.label')}{/if}
        </button>
        <button
            type="button"
            class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border
                   text-text hover:bg-bg-panel disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={!!busy}
            on:click={onRestart}
            title={$t('sessionView.restart.title')}>
            {busyMatches('Restart') ? '…' : '🔄'} {$t('sessionView.restart.label')}
        </button>

        {#if lastError}
            <span class="text-status-error text-xs ml-2">{lastError}</span>
        {/if}
    </div>

    <SessionInput {session} />

    {#if restartDialog}
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
             data-testid="restart-dialog">
            <div class="bg-bg-panel border border-bg-border rounded p-4 w-80 space-y-3">
                <div class="text-sm font-medium text-text">{$t('sessionView.restart.dialogTitle')}</div>
                <div class="flex flex-col gap-2">
                    <button type="button" class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg-panel"
                            data-testid="restart-continue" on:click={() => doRestart(false)}>
                        {$t('sessionView.restart.continue')}
                    </button>
                    <button type="button" class="px-2.5 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg-panel"
                            data-testid="restart-fresh" on:click={() => doRestart(true)}>
                        {$t('sessionView.restart.fresh')}
                    </button>
                    <button type="button" class="px-2.5 py-1 text-xs rounded text-text-muted hover:bg-bg-panel"
                            on:click={() => (restartDialog = false)}>
                        {$t('sessionView.restart.cancel')}
                    </button>
                </div>
            </div>
        </div>
    {/if}
</div>
