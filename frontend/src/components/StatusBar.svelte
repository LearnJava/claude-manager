<script lang="ts">
    import { onMount, onDestroy, createEventDispatcher } from 'svelte';
    import {
        sessionList,
        activeSessions,
        waitingSessions,
        rateLimitedSessions,
        errorSessions,
        todayCost,
        todayTokens,
        appStartedAt,
        rateLimitStatus,
        regressionAlerts,
    } from '../stores/sessions';
    import { costUnit, toggleCostUnit } from '../stores/units';
    import { formatDuration, formatTokens, tokenSplitLabel } from '../lib/formatters';
    import { billing } from '../stores/billing';
    import { limitTokens, windowShare, formatWindowShare } from '../lib/billing';
    import { t } from '../lib/i18n';
    import { waitingItems, waitingScreenEnabled } from '../stores/waiting';

    const dispatch = createEventDispatcher();

    // ticking clock for uptime
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

    function fmtUptime(startMs: number, nowMs: number): string {
        const ms = Math.max(0, nowMs - startMs);
        const s = Math.floor(ms / 1000);
        const h = Math.floor(s / 3600);
        const m = Math.floor((s % 3600) / 60);
        const sec = s % 60;
        if (h > 0) return `${h}h ${m}m`;
        if (m > 0) return `${m}m ${sec}s`;
        return `${sec}s`;
    }

    function fmtCost(n: number): string {
        if (!n || isNaN(n)) return '$0.00';
        return `$${n.toFixed(2)}`;
    }

    function fmtUtil(u: number | undefined): string {
        if (u === undefined || u === null || isNaN(u)) return '';
        return `${Math.round(u * 100)}%`;
    }

    // Subscription mode: today's limit tokens (input + cache write + cache read),
    // their share of the window and the time they took lead (WS-02).
    $: subscription = $billing.mode === 'subscription';
    $: todayLimit = limitTokens($todayTokens);
    $: todayShare = windowShare(todayLimit, $billing.windowTokens);
    $: total = $sessionList.length;
    $: active = $activeSessions.length;
    $: waiting = $waitingSessions.length;
    $: rateLimited = $rateLimitedSessions.length;
    $: errors = $errorSessions.length;
    $: uptimeStr = fmtUptime($appStartedAt.getTime(), now);
    $: rlInfo = $rateLimitStatus;
    $: rlUtil = rlInfo ? (rlInfo.utilization ?? rlInfo.Utilization) : undefined;
    $: regressions = $regressionAlerts.length;
    // The button exists only with [optimization] waiting_screen on; at zero it
    // still opens the screen (an empty list reads as "all clear").
    $: waitingForYou = $waitingItems.length;

    function openQueue() {
        dispatch('openQueue');
    }

    function openWaiting() {
        dispatch('openWaiting');
    }

    function openRegressions() {
        dispatch('openDashboard');
    }
</script>

<footer class="h-7 bg-bg-panel border-t border-bg-border px-3 flex items-center text-xs text-text-muted gap-4 select-none">
    <span>
        {$t('statusBar.activeLabel')}
        <span class="text-text font-medium">{active}/{total}</span>
    </span>

    {#if waiting > 0}
        <button
            class="text-status-waiting hover:underline focus:outline-none flex items-center gap-1"
            on:click={openQueue}
            title={$t('statusBar.openQueueTooltip')}
            type="button">
            <span>⚠</span>
            <span>{$t('statusBar.waitingLabel', { count: waiting })}</span>
        </button>
    {/if}

    {#if $waitingScreenEnabled}
        <button
            class="{waitingForYou > 0 ? 'text-status-waiting' : ''} hover:underline focus:outline-none flex items-center gap-1"
            data-testid="open-waiting"
            on:click={openWaiting}
            title={$t('waiting.statusTooltip')}
            type="button">
            <span>🙋</span>
            <span>{$t('waiting.statusLabel', { count: waitingForYou })}</span>
        </button>
    {/if}

    <span class={rateLimited > 0 ? 'text-status-ratelimit' : ''}>
        {$t('statusBar.rateLimitedLabel', { count: rateLimited })}
    </span>

    <span class={errors > 0 ? 'text-status-error' : ''}>
        {$t('statusBar.errorsLabel', { count: errors })}
    </span>

    {#if regressions > 0}
        <button
            class="text-status-error hover:underline focus:outline-none flex items-center gap-1"
            on:click={openRegressions}
            title={$t('statusBar.openDashboardTooltip')}
            type="button">
            <span>⚠</span>
            <span>{$t('statusBar.regressionsLabel', { count: regressions })}</span>
        </button>
    {/if}

    <!--
        Tokens lead, dollars follow. Click switches which one is the headline;
        the other stays visible either way, so the switch changes emphasis, not
        available information. Tooltip carries the in/out/cache split — the
        total alone hides that a cache read is ~10x cheaper than fresh input.
    -->
    {#if subscription}
        <span
            data-testid="today-subscription"
            title={$t('statusBar.todaySubscriptionTooltip', { split: tokenSplitLabel($todayTokens) })}>
            {$t('statusBar.todayLabel')}
            <span class="text-text">{formatTokens(todayLimit)}</span> {$t('statusBar.tokUnit')}
            <span data-testid="today-share" class={todayShare === null ? 'opacity-70' : 'text-text'}>
                ({todayShare === null
                    ? $t('statusBar.windowUnknown')
                    : $t('statusBar.windowShareOf', { pct: formatWindowShare(todayShare, '') })})
            </span>
            · <span class="text-text" data-testid="today-time">{formatDuration($todayTokens.duration_ms)}</span>
            <span class="opacity-60">({$t('statusBar.costConventional', { cost: fmtCost($todayCost) })})</span>
        </span>
    {:else}
        <button
            class="hover:underline focus:outline-none"
            on:click={toggleCostUnit}
            type="button"
            title={$t('statusBar.todayTooltip', { split: tokenSplitLabel($todayTokens) })}>
            {#if $costUnit === 'tokens'}
                {$t('statusBar.todayLabel')} <span class="text-text">{formatTokens($todayTokens.total)}</span> {$t('statusBar.tokUnit')}
                <span class="opacity-60">({fmtCost($todayCost)})</span>
            {:else}
                {$t('statusBar.todayLabel')} <span class="text-text">{fmtCost($todayCost)}</span>
                <span class="opacity-60">({formatTokens($todayTokens.total)} {$t('statusBar.tokUnit')})</span>
            {/if}
        </button>
    {/if}

    {#if rlUtil !== undefined}
        <span title={$t('statusBar.rlTooltip')}>
            {$t('statusBar.rlLabel')} <span class={rlUtil >= 0.85 ? 'text-status-ratelimit' : 'text-text'}>{fmtUtil(rlUtil)}</span>
        </span>
    {/if}

    <span class="ml-auto">
        {$t('statusBar.uptimeLabel')} <span class="text-text">{uptimeStr}</span>
    </span>
</footer>
