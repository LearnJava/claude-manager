<script lang="ts">
    // "Queue" tab of the TaskPanel: only what the session's status file points
    // at — the current row as a card, the rest as a numbered list. The whole
    // backlog stays in the Roadmap tab.
    import type { SessionState } from '../stores/sessions';
    import { queues } from '../stores/queues';
    import { EMPTY_QUEUE, hasQueue, queuedCount, sizeChipClass } from '../lib/queue';
    import { t } from '../lib/i18n';

    export let session: SessionState;

    $: snap = $queues[session.id];
    $: model = snap?.model ?? EMPTY_QUEUE;
    $: total = queuedCount(model) + session.tasks_done;

    // The in-progress TodoWrite step, same source as the Task tab.
    $: todos = session.todos ?? [];
    $: step = todos.find((x) => x.status === 'in_progress');
    $: stepIndex = step ? todos.indexOf(step) + 1 : 0;
    $: running = session.status !== 'idle' && session.status !== 'error';
    $: percent =
        todos.length > 0
            ? Math.round(
                  ((todos.filter((x) => x.status === 'completed').length + (step ? 0.5 : 0)) /
                      todos.length) *
                      100,
              )
            : 0;
</script>

<div class="space-y-3" data-testid="queue-panel">
    {#if !snap || (snap.loading && !hasQueue(model))}
        <div class="text-xs text-text-muted">{$t('queue.loading')}</div>
    {:else if snap.error && !hasQueue(model)}
        <div class="text-xs text-status-error break-words">{snap.error}</div>
    {:else if !hasQueue(model)}
        <div class="text-xs text-text-muted">{$t('queue.empty')}</div>
    {:else}
        {#if model.current}
            <div
                class="rounded border border-status-working bg-status-working/10 px-2 py-2"
                data-testid="queue-current">
                <div class="text-[10px] uppercase tracking-wide text-status-working">
                    {$t(running ? 'queue.now' : 'queue.next', {
                        position: session.tasks_done + 1,
                        total,
                    })}
                </div>
                <div class="mt-0.5 text-xs font-semibold text-text break-words">
                    {#if model.current.id}<span class="font-mono text-text-muted">{model.current.id}</span> · {/if}{model.current.name}
                    {#if model.current.size}
                        <span class="ml-1 text-[10px] font-normal {sizeChipClass(model.current.size)}"
                              title={$t('queue.sizeTitle', { size: model.current.size })}>{model.current.size}</span>
                    {/if}
                </div>
                {#if model.current.summary}
                    <div class="mt-0.5 text-[11px] text-text-muted break-words">{model.current.summary}</div>
                {/if}
                {#if running && step}
                    <div class="mt-1 text-[10px] text-status-working break-words">
                        ▸ {$t('queue.step')} {stepIndex}/{todos.length}: {step.activeForm || step.content}
                    </div>
                    <div class="mt-1 h-1.5 rounded bg-bg-elevated overflow-hidden">
                        <div class="h-full rounded bg-status-working transition-all duration-500"
                             style="width: {percent}%"></div>
                    </div>
                {/if}
            </div>
        {/if}

        {#if model.upcoming.length > 0}
            <div>
                <div class="text-[10px] uppercase tracking-wide text-text-muted mb-1">
                    {$t('queue.upcoming')}
                </div>
                <ol class="divide-y divide-bg-border" data-testid="queue-upcoming">
                    {#each model.upcoming as item, i (item.id + ':' + i)}
                        <li class="flex items-start gap-2 py-1.5 text-xs leading-snug">
                            <span class="shrink-0 w-[18px] h-[18px] rounded-full bg-bg-elevated
                                         text-[10px] text-text-muted text-center leading-[18px]">{i + 1}</span>
                            <div class="min-w-0 {item.status === 'blocked' ? 'opacity-70' : ''}">
                                <div class="text-text break-words">
                                    {#if item.id}<span class="font-mono text-text-muted">{item.id}</span> · {/if}{item.name}
                                    {#if item.size}
                                        <span class="ml-1 text-[10px] {sizeChipClass(item.size)}"
                                              title={$t('queue.sizeTitle', { size: item.size })}>{item.size}</span>
                                    {/if}
                                    {#if item.status === 'blocked'}
                                        <span class="ml-1 text-[10px] text-status-error">⊘ {$t('queue.blocked')}</span>
                                    {/if}
                                </div>
                                {#if item.waitsFor.length > 0}
                                    <div class="text-[10px] text-text-muted">
                                        ⛓ {$t('queue.waitsFor', { ids: item.waitsFor.join(', ') })}
                                    </div>
                                {/if}
                            </div>
                        </li>
                    {/each}
                </ol>
            </div>
        {/if}

        {#if model.doneCount > 0}
            <div class="text-[10px] text-text-dim">
                {$t('queue.doneInRoadmap', { count: model.doneCount })}
            </div>
        {/if}
    {/if}
</div>
