<script lang="ts">
    import type { SessionState, TodoItem } from '../stores/sessions';
    import RoadmapTree from './RoadmapTree.svelte';
    import QueuePanel from './QueuePanel.svelte';
    import { queues } from '../stores/queues';
    import { EMPTY_QUEUE, hasQueue, queuedCount } from '../lib/queue';
    import { t } from '../lib/i18n';

    export let session: SessionState;

    let showPrompt = false;

    // Three tabs: "Task" is Claude's live TodoWrite breakdown for the step it is
    // on right now; "Queue" is the slice of the backlog this session's status
    // file points at; "Roadmap" is the project's whole backlog. A task_source
    // session spends most of its life inside one queued task, so Queue is the
    // default there; with no queue but a task source the roadmap is, and an
    // ad-hoc/chat session has neither.
    type Tab = 'task' | 'queue' | 'roadmap';
    type TabDef = { id: Tab; label: string };
    $: model = $queues[session.id]?.model ?? EMPTY_QUEUE;
    $: queueVisible = hasQueue(model);
    $: tabs = [
        { id: 'task', label: $t('taskPanel.tabs.task') },
        ...(queueVisible
            ? [{ id: 'queue', label: `${$t('queue.tab')} · ${queuedCount(model)}` }]
            : []),
        { id: 'roadmap', label: $t('taskPanel.tabs.roadmap') },
    ] as TabDef[];
    // The tab the user picked for the session they are looking at; null means
    // "use the default", which keeps following the queue as it loads.
    let chosen: Tab | null = null;
    let tabForSession = '';
    $: if (session.id !== tabForSession) {
        tabForSession = session.id;
        chosen = null;
    }
    $: defaultTab = (queueVisible ? 'queue' : session.task_source_description ? 'roadmap' : 'task') as Tab;
    $: tab = chosen && tabs.some((x) => x.id === chosen) ? chosen : defaultTab;

    $: todos = (session.todos ?? []) as TodoItem[];
    $: total = todos.length;
    $: completed = todos.filter((t) => t.status === 'completed').length;
    // Count the in_progress item as half done so the bar moves as soon as
    // Claude starts a step, not only when it finishes one.
    $: inProgress = todos.filter((t) => t.status === 'in_progress').length;
    $: percent = total > 0 ? Math.round(((completed + inProgress * 0.5) / total) * 100) : 0;

    // Precedence: Claude's own TodoWrite breakdown (most specific and live)
    // beats the task_source-resolved description (coarser — a ROADMAP.md row
    // or task-file heading — but available before the first TodoWrite call),
    // which beats a bare "Working…" placeholder.
    $: currentLabel =
        session.current_task ||
        session.task_source_description ||
        (session.status === 'working' ? 'Working…' : '');
    $: currentLabelFromSource = !session.current_task && !!session.task_source_description;

    function icon(t: TodoItem): string {
        if (t.status === 'completed') return '✓';
        if (t.status === 'in_progress') return '▸';
        return '○';
    }

    function label(t: TodoItem): string {
        return t.status === 'in_progress' && t.activeForm ? t.activeForm : t.content;
    }
</script>

<div class="w-72 shrink-0 border-l border-bg-border bg-bg-panel flex flex-col min-h-0">
    <div class="px-3 py-2 border-b border-bg-border flex items-center justify-between gap-2">
        <div class="flex items-center gap-1">
            {#each tabs as t}
                <button
                    type="button"
                    on:click={() => (chosen = t.id)}
                    class="px-1.5 py-0.5 text-xs uppercase tracking-wide rounded
                           {tab === t.id
                        ? 'text-text font-semibold bg-bg-elevated'
                        : 'text-text-muted hover:text-text'}">
                    {t.label}
                </button>
            {/each}
        </div>
        {#if tab === 'task' && total > 0}
            <span class="text-xs text-text-muted">{completed}/{total} · {percent}%</span>
        {/if}
    </div>

    {#if tab === 'queue'}
        <div class="flex-1 min-h-0 overflow-y-auto px-3 py-2">
            <QueuePanel {session} />
        </div>
    {:else if tab === 'roadmap'}
        <div class="flex-1 min-h-0 overflow-y-auto px-3 py-2">
            <RoadmapTree {session} />
        </div>
    {:else}
    <div class="flex-1 min-h-0 overflow-y-auto px-3 py-2 space-y-3">
        {#if total > 0}
            <!-- Progress bar -->
            <div class="h-1.5 rounded bg-bg-elevated overflow-hidden">
                <div
                    class="h-full rounded bg-status-working transition-all duration-500"
                    style="width: {percent}%"></div>
            </div>
        {/if}

        {#if currentLabel}
            <div>
                <div class="text-[10px] uppercase tracking-wide text-text-muted mb-0.5">
                    {currentLabelFromSource ? $t('taskPanel.nowFromSource') : $t('taskPanel.now')}
                </div>
                <div class="text-xs text-text break-words">{currentLabel}</div>
            </div>
        {/if}

        {#if total > 0}
            <ul class="space-y-1">
                {#each todos as t}
                    <li class="flex items-start gap-1.5 text-xs leading-snug">
                        <span
                            class:text-status-working={t.status === 'in_progress'}
                            class:text-text-muted={t.status !== 'in_progress'}
                            class="shrink-0 w-3 text-center">{icon(t)}</span>
                        <span
                            class:line-through={t.status === 'completed'}
                            class:text-text-muted={t.status === 'completed'}
                            class:text-text={t.status !== 'completed'}
                            class="break-words">{label(t)}</span>
                    </li>
                {/each}
            </ul>
        {:else if !currentLabel}
            <div class="text-xs text-text-muted">
                {$t('taskPanel.noActivity')}
            </div>
        {/if}

        {#if (session.prompt ?? '').trim()}
            <div class="pt-2 border-t border-bg-border">
                <button
                    type="button"
                    class="text-[10px] uppercase tracking-wide text-text-muted hover:text-text"
                    on:click={() => (showPrompt = !showPrompt)}>
                    {showPrompt ? '− ' : '+ '}{$t('taskPanel.sessionPrompt')}
                </button>
                {#if showPrompt}
                    <div class="mt-1 text-xs text-text-muted whitespace-pre-wrap break-words">
                        {session.prompt}
                    </div>
                {/if}
            </div>
        {/if}
    </div>
    {/if}
</div>
