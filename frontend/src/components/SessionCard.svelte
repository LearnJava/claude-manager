<script lang="ts">
    import { onMount, onDestroy } from 'svelte';
    import type { SessionState, SessionStatus } from '../stores/sessions';
    import {
        formatCost,
        formatDuration,
        formatTokens,
        formatPercent,
        cacheHitRatio,
        contextBarColor,
        tokenSplit,
        tokenSplitLabel,
    } from '../lib/formatters';
    import { costUnit } from '../stores/units';
    import { t } from '../lib/i18n';

    export let session: SessionState;

    // Ticking clock so the runtime display refreshes once per second.
    let now = Date.now();
    let tick: ReturnType<typeof setInterval>;

    onMount(() => {
        tick = setInterval(() => {
            now = Date.now();
        }, 1000);
    });

    onDestroy(() => {
        if (tick) clearInterval(tick);
    });

    // Guards against Go's zero time.Time ("0001-01-01T00:00:00Z"), which is a
    // non-empty, validly-parsing string that would otherwise produce a
    // multi-million-hour runtime instead of the intended "not started" 0.
    const MIN_SANE_STARTED_AT_MS = Date.UTC(2000, 0, 1);

    function runtimeMs(s: SessionState, nowMs: number): number {
        if (!s.started_at) return 0;
        const t = new Date(s.started_at).getTime();
        if (!t || isNaN(t) || t < MIN_SANE_STARTED_AT_MS) return 0;
        return Math.max(0, nowMs - t);
    }

    function statusDot(status: SessionStatus): string {
        switch (status) {
            case 'working': return 'bg-status-working';
            case 'waiting_permission':
            case 'waiting_for_user': return 'bg-status-waiting';
            case 'rate_limited':
            case 'retrying': return 'bg-status-ratelimit';
            case 'error': return 'bg-status-error';
            case 'starting':
            case 'analyzing': return 'bg-status-starting';
            case 'stopping':
            case 'idle':
            default: return 'bg-status-idle';
        }
    }

    function statusLabel(status: SessionStatus, tr: (key: string, params?: Record<string, string | number>) => string): string {
        switch (status) {
            case 'working': return tr('sessionCard.statusWorking');
            case 'waiting_permission': return tr('sessionCard.statusWaitingPermission');
            case 'waiting_for_user': return tr('sessionCard.statusWaitingForAnswer');
            case 'rate_limited': return tr('sessionCard.statusRateLimited');
            case 'retrying': return tr('sessionCard.statusRetrying');
            case 'error': return tr('sessionCard.statusError');
            case 'starting': return tr('sessionCard.statusStarting');
            case 'analyzing': return tr('sessionCard.statusAnalyzing');
            case 'stopping': return tr('sessionCard.statusStopping');
            case 'idle': return tr('sessionCard.statusIdle');
            default: return status;
        }
    }

    $: runtime = formatDuration(runtimeMs(session, now));
    $: hit = cacheHitRatio(session.cache_read, session.cache_creation);
    $: split = tokenSplit(session);
    $: util = Number(session.context_util) || 0;
    $: utilPct = Math.min(100, Math.max(0, Math.round(util * 100)));
    $: blink = session.status === 'waiting_permission' || session.status === 'waiting_for_user';
</script>

<div class="bg-bg-panel border border-bg-border rounded px-3 py-2 text-sm">
    <!-- Row 1: name / project + status + runtime -->
    <div class="flex items-center gap-3">
        <span
            class="w-2.5 h-2.5 rounded-full shrink-0 {statusDot(session.status)}
                   {blink ? 'dot-blink' : ''}"
            title={statusLabel(session.status, $t)}></span>
        <div class="flex items-baseline gap-2 min-w-0">
            <h2 class="text-text font-semibold truncate">{session.name}</h2>
            <span class="text-text-muted text-xs truncate">— {session.project}</span>
        </div>
        <span class="text-text-muted text-xs ml-auto whitespace-nowrap">
            {statusLabel(session.status, $t)}
            {#if session.stop_requested}
                <span class="text-amber-400" title={$t('sessionCard.stopRequestedTooltip')}>
                    {$t('sessionCard.stoppingAfterTask')}
                </span>
            {/if}
        </span>
        <span class="text-text-muted text-xs whitespace-nowrap" title={$t('sessionCard.runtimeTooltip')}>
            {runtime}
        </span>
    </div>

    <!-- Row 2: branch / task / turns / tokens -->
    <div class="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-text-muted">
        {#if session.branch}
            <span>{$t('sessionCard.branchLabel')} <span class="text-text">{session.branch}</span></span>
        {/if}
        {#if session.current_task}
            <span>{$t('sessionCard.taskLabel')} <span class="text-text">{session.current_task}</span></span>
        {/if}
        <span>{$t('sessionCard.turnsLabel')} <span class="text-text">{session.num_turns ?? 0}</span></span>
        <span title={tokenSplitLabel(session)}>
            {$t('sessionCard.tokensLabel')}
            <span class="text-text">{formatTokens(split.total)}</span>
            <span class="opacity-70">
                {$t('sessionCard.tokensBreakdown', { input: formatTokens(split.input), output: formatTokens(split.output), cacheRead: formatTokens(split.cacheRead) })}
            </span>
        </span>
        {#if session.model}
            <span>{$t('sessionCard.modelLabel')} <span class="text-text">{session.model}</span></span>
        {/if}
    </div>

    <!-- Row 3: cost / cache hit / context bar -->
    <div class="mt-1 flex items-center gap-x-4 text-xs text-text-muted">
        <!-- Dollars stay on screen but step back when tokens are the chosen unit. -->
        <span>
            {$t('sessionCard.costLabel')}
            <span class={$costUnit === 'usd' ? 'text-text' : 'opacity-70'}>
                {formatCost(session.total_cost_usd)}
            </span>
        </span>
        <span>{$t('sessionCard.cacheHitLabel')} <span class="text-text">{formatPercent(hit)}</span></span>

        <div class="flex items-center gap-2 ml-auto min-w-[160px]">
            <span class="whitespace-nowrap">{$t('sessionCard.contextLabel')}</span>
            <div
                class="relative flex-1 h-2 bg-bg-elevated rounded overflow-hidden"
                title={$t('sessionCard.contextTooltip')}>
                <div
                    class="h-full {contextBarColor(util)} transition-all"
                    style="width: {utilPct}%"></div>
            </div>
            <span
                class="w-9 text-right tabular-nums {util >= 0.8 ? 'text-status-error' : util >= 0.6 ? 'text-status-ratelimit' : 'text-text'}">
                {utilPct}%
            </span>
        </div>
    </div>
</div>
