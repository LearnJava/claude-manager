<script lang="ts">
    // Body of the "waiting for you" overlay (WASTE-TASKS.md WS-08): one row per
    // session that needs a person, with the reason and a jump to it. The overlay
    // frame (backdrop, Escape, close button) lives in App.svelte, same as the
    // permission queue's.
    import { onMount, onDestroy, createEventDispatcher } from 'svelte';
    import { waitingItems } from '../stores/waiting';
    import type { WaitingItem, WaitingKind } from '../lib/waiting';
    import { t } from '../lib/i18n';

    const dispatch = createEventDispatcher<{
        goto: { sessionId: string };
        openPermissions: void;
        openMixed: void;
    }>();

    let now = Date.now();
    let tick: ReturnType<typeof setInterval>;
    onMount(() => {
        tick = setInterval(() => (now = Date.now()), 1000);
    });
    onDestroy(() => {
        if (tick) clearInterval(tick);
    });

    const ICON: Record<WaitingKind, string> = {
        permission: '🔐',
        question: '❓',
        gates_exhausted: '🔁',
        error: '⛔',
        needs_human: '🙋',
        rate_limit: '⏳',
        stopped: '⏹',
        queue_empty: '📭',
    };

    function span(ms: number): string {
        const sec = Math.max(0, Math.floor(ms / 1000));
        if (sec < 60) return `${sec}s`;
        const m = Math.floor(sec / 60);
        if (m < 60) return `${m}m ${sec % 60}s`;
        return `${Math.floor(m / 60)}h ${m % 60}m`;
    }

    function when(item: WaitingItem, nowMs: number): string {
        if (item.since === null) return '';
        if (item.kind === 'rate_limit') {
            return item.since > nowMs ? $t('waiting.resumesIn', { time: span(item.since - nowMs) }) : '';
        }
        return span(nowMs - item.since);
    }

    function go(item: WaitingItem) {
        if (item.kind === 'needs_human') dispatch('openMixed');
        else dispatch('goto', { sessionId: item.sessionId });
    }
</script>

<div class="flex-1 min-h-0 overflow-y-auto" data-testid="waiting-screen">
    {#if $waitingItems.length === 0}
        <div class="text-text-muted text-sm italic py-6 text-center" data-testid="waiting-empty">
            {$t('waiting.empty')}
        </div>
    {:else}
        <ul class="divide-y divide-bg-border">
            {#each $waitingItems as item (item.key)}
                <li class="py-2 px-1" data-testid="waiting-row" data-kind={item.kind}>
                    <div class="flex items-baseline gap-2 text-sm">
                        <span>{ICON[item.kind]}</span>
                        <span class="font-semibold text-text">
                            {item.sessionId ? item.sessionId.slice(item.project.length + 1) : item.taskId}
                        </span>
                        <span class="text-text-muted text-xs">— {item.project}</span>
                        <span class="text-text-muted text-xs ml-auto tabular-nums whitespace-nowrap">{when(item, now)}</span>
                    </div>
                    <div class="mt-1 text-sm text-text">{$t(`waiting.kind.${item.kind}`)}</div>
                    {#if item.detail}
                        <div class="mt-0.5 text-xs text-text-muted font-mono break-all">{item.detail}</div>
                    {/if}
                    <div class="mt-2 flex flex-wrap gap-2">
                        {#if item.kind === 'permission'}
                            <button
                                type="button"
                                data-testid="waiting-answer"
                                on:click={() => dispatch('openPermissions')}
                                class="px-2.5 py-1 text-xs rounded font-medium bg-status-waiting/90 hover:bg-status-waiting text-white">
                                {$t('waiting.action.permission')}
                            </button>
                        {/if}
                        <button
                            type="button"
                            data-testid="waiting-go"
                            on:click={() => go(item)}
                            class="px-2.5 py-1 text-xs rounded font-medium border border-bg-border text-text hover:bg-bg-elevated">
                            {item.kind === 'needs_human' ? $t('waiting.action.mixed') : $t('waiting.action.session')}
                        </button>
                    </div>
                </li>
            {/each}
        </ul>
    {/if}
</div>
