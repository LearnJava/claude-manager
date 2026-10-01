<script lang="ts">
    // Sidebar row footer: one segment per task (done / current / queued) plus
    // exact counts; a click unfolds the list of what is still ahead. Lets you
    // read every developer's queue without opening the session.
    import type { SessionState } from '../stores/sessions';
    import { queues } from '../stores/queues';
    import { EMPTY_QUEUE, hasQueue, queueSegments } from '../lib/queue';
    import { t } from '../lib/i18n';

    export let session: SessionState;
    export let running = false;

    let open = false;

    $: model = $queues[session.id]?.model ?? EMPTY_QUEUE;
    $: segments = queueSegments(model, session.tasks_done ?? 0);

    function toggle(e: Event) {
        // The row itself selects the session; this must not.
        e.stopPropagation();
        open = !open;
    }
</script>

{#if hasQueue(model)}
    <div class="mt-1" data-testid="queue-mini-{session.id}">
        <button
            type="button"
            class="w-full text-left"
            title={$t('queue.mini.toggleTitle')}
            aria-expanded={open}
            on:click={toggle}
            on:keydown={(e) => e.stopPropagation()}>
            <div class="flex gap-0.5">
                {#each segments as seg}
                    <span
                        class="flex-1 h-1 rounded-sm
                               {seg === 'done'
                            ? 'bg-status-working'
                            : seg === 'current'
                              ? 'bg-status-ratelimit'
                              : 'bg-bg-border'}"></span>
                {/each}
            </div>
            <div class="mt-0.5 flex items-center text-text-muted" style="font-size: 10px">
                <span class="flex-1 truncate">
                    {#if (session.tasks_done ?? 0) > 0}{$t('queue.mini.done', { count: session.tasks_done })} · {/if}{#if model.current}<span class={running ? 'text-status-working' : ''}>{$t(running ? 'queue.mini.running' : 'queue.mini.next', { count: 1 })}</span>{#if model.upcoming.length > 0} · {/if}{/if}{#if model.upcoming.length > 0}{$t('queue.mini.queued', { count: model.upcoming.length })}{/if}
                </span>
                <span>{open ? '▴' : '▾'}</span>
            </div>
        </button>
        {#if open}
            <ul
                class="mt-1 p-1.5 rounded border border-bg-border bg-bg space-y-0.5"
                style="font-size: 11px"
                data-testid="queue-mini-list-{session.id}">
                {#if model.current}
                    <li class="text-text font-semibold truncate" title={model.current.name}>
                        <span class="text-status-working">▸</span>
                        {#if model.current.id}<span class="font-mono">{model.current.id}</span> {/if}{model.current.name}
                    </li>
                {/if}
                {#each model.upcoming as item, i (item.id + ':' + i)}
                    <li class="text-text-muted truncate" title={item.name}>
                        ○ {#if item.id}<span class="font-mono">{item.id}</span> {/if}{item.name}
                    </li>
                {/each}
            </ul>
        {/if}
    </div>
{/if}
