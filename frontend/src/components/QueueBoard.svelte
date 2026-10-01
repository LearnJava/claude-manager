<script lang="ts">
    // Board view of a session's queue, shown in place of the log stream:
    // Queue → In progress → Done, plus a Blocked column when something is.
    // Read-only — the queue is edited in the task source file, not here.
    import type { SessionState } from '../stores/sessions';
    import { queues } from '../stores/queues';
    import { EMPTY_QUEUE, sizeChipClass, type QueueItem } from '../lib/queue';
    import { t } from '../lib/i18n';

    export let session: SessionState;

    $: model = $queues[session.id]?.model ?? EMPTY_QUEUE;
    $: waiting = model.upcoming.filter((i) => i.status !== 'blocked');
    $: blocked = model.upcoming.filter((i) => i.status === 'blocked');
    $: doneHidden = Math.max(0, model.doneCount - model.doneRecent.length);
    $: cols = blocked.length > 0 ? 4 : 3;

    $: running = session.status !== 'idle' && session.status !== 'error';
    $: todos = session.todos ?? [];
    $: step = todos.find((x) => x.status === 'in_progress');
    $: percent =
        todos.length > 0
            ? Math.round(
                  ((todos.filter((x) => x.status === 'completed').length + (step ? 0.5 : 0)) /
                      todos.length) *
                      100,
              )
            : 0;

    function title(i: QueueItem): string {
        return i.summary ? `${i.name} — ${i.summary}` : i.name;
    }
</script>

<div class="flex-1 min-h-0 overflow-auto p-3" data-testid="queue-board">
    <div class="grid gap-3 min-h-full" style="grid-template-columns: repeat({cols}, minmax(0, 1fr))">
        <!-- Queue -->
        <section class="rounded border border-bg-border bg-bg p-2 flex flex-col gap-2" data-testid="queue-col-queue">
            <header class="flex justify-between text-xs font-semibold text-text">
                <span>{$t('queue.board.queue')}</span><span class="text-text-muted">{waiting.length}</span>
            </header>
            {#each waiting as item, i (item.id + ':' + i)}
                <article class="rounded border border-bg-border bg-bg-elevated p-2 text-xs" title={title(item)}>
                    <div class="font-mono text-[10px] text-text-muted">
                        {item.id}{#if item.waitsFor.length > 0} ⛓ {item.waitsFor.join(', ')}{/if}
                    </div>
                    <div class="text-text break-words">
                        {item.name}
                        {#if item.size}<span class="ml-1 text-[10px] {sizeChipClass(item.size)}">{item.size}</span>{/if}
                    </div>
                </article>
            {:else}
                <div class="text-xs text-text-dim text-center my-auto">{$t('queue.board.emptyColumn')}</div>
            {/each}
        </section>

        <!-- In progress -->
        <section class="rounded border border-bg-border bg-bg p-2 flex flex-col gap-2" data-testid="queue-col-running">
            <header class="flex justify-between text-xs font-semibold text-status-working">
                <span>{$t('queue.board.running')}</span><span class="text-text-muted">{model.current ? 1 : 0}</span>
            </header>
            {#if model.current}
                <article class="rounded border border-status-working bg-bg-elevated p-2 text-xs" title={title(model.current)}>
                    <div class="font-mono text-[10px] text-text-muted">
                        {model.current.id}{#if session.branch} · {session.branch}{/if}
                    </div>
                    <div class="text-text break-words">
                        {model.current.name}
                        {#if model.current.size}<span class="ml-1 text-[10px] {sizeChipClass(model.current.size)}">{model.current.size}</span>{/if}
                    </div>
                    {#if running && todos.length > 0}
                        <div class="mt-1.5 h-1.5 rounded bg-bg overflow-hidden">
                            <div class="h-full rounded bg-status-working transition-all duration-500"
                                 style="width: {percent}%"></div>
                        </div>
                        {#if step}
                            <div class="mt-1 text-[10px] text-text-muted break-words">
                                {step.activeForm || step.content}
                            </div>
                        {/if}
                    {/if}
                </article>
            {:else}
                <div class="text-xs text-text-dim text-center my-auto">{$t('queue.board.emptyColumn')}</div>
            {/if}
        </section>

        <!-- Blocked (only when something is) -->
        {#if blocked.length > 0}
            <section class="rounded border border-bg-border bg-bg p-2 flex flex-col gap-2" data-testid="queue-col-blocked">
                <header class="flex justify-between text-xs font-semibold text-status-error">
                    <span>{$t('queue.board.blocked')}</span><span class="text-text-muted">{blocked.length}</span>
                </header>
                {#each blocked as item, i (item.id + ':' + i)}
                    <article class="rounded border border-status-error/40 bg-bg-elevated p-2 text-xs" title={title(item)}>
                        <div class="font-mono text-[10px] text-text-muted">{item.id}</div>
                        <div class="text-text break-words">{item.name}</div>
                    </article>
                {/each}
            </section>
        {/if}

        <!-- Done -->
        <section class="rounded border border-bg-border bg-bg p-2 flex flex-col gap-2" data-testid="queue-col-done">
            <header class="flex justify-between text-xs font-semibold text-text">
                <span>{$t('queue.board.done')}</span><span class="text-text-muted">{model.doneCount}</span>
            </header>
            {#each model.doneRecent as item, i (item.id + ':' + i)}
                <article class="rounded border border-bg-border bg-bg-elevated p-2 text-xs opacity-60" title={title(item)}>
                    <div class="font-mono text-[10px] text-text-muted">{item.id} ✓</div>
                    <div class="text-text break-words">{item.name}</div>
                </article>
            {:else}
                <div class="text-xs text-text-dim text-center my-auto">{$t('queue.board.emptyColumn')}</div>
            {/each}
            {#if doneHidden > 0}
                <div class="text-[10px] text-text-dim text-center">{$t('queue.board.more', { count: doneHidden })}</div>
            {/if}
        </section>
    </div>
</div>
