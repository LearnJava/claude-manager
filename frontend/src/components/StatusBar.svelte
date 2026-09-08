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
    } from '../stores/sessions';
    import { costUnit, toggleCostUnit } from '../stores/units';
    import { formatTokens, tokenSplitLabel } from '../lib/formatters';

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

    $: total = $sessionList.length;
    $: active = $activeSessions.length;
    $: waiting = $waitingSessions.length;
    $: rateLimited = $rateLimitedSessions.length;
    $: errors = $errorSessions.length;
    $: uptimeStr = fmtUptime($appStartedAt.getTime(), now);
    $: rlInfo = $rateLimitStatus;
    $: rlUtil = rlInfo ? (rlInfo.utilization ?? rlInfo.Utilization) : undefined;

    function openQueue() {
        dispatch('openQueue');
    }
</script>

<footer class="h-7 bg-bg-panel border-t border-bg-border px-3 flex items-center text-xs text-text-muted gap-4 select-none">
    <span>
        Active:
        <span class="text-text font-medium">{active}/{total}</span>
    </span>

    {#if waiting > 0}
        <button
            class="text-status-waiting hover:underline focus:outline-none flex items-center gap-1"
            on:click={openQueue}
            title="Open permission queue"
            type="button">
            <span>⚠</span>
            <span>Waiting: {waiting}</span>
        </button>
    {/if}

    <span class={rateLimited > 0 ? 'text-status-ratelimit' : ''}>
        Rate limited: {rateLimited}
    </span>

    <span class={errors > 0 ? 'text-status-error' : ''}>
        Errors: {errors}
    </span>

    <!--
        Tokens lead, dollars follow. Click switches which one is the headline;
        the other stays visible either way, so the switch changes emphasis, not
        available information. Tooltip carries the in/out/cache split — the
        total alone hides that a cache read is ~10x cheaper than fresh input.
    -->
    <button
        class="hover:underline focus:outline-none"
        on:click={toggleCostUnit}
        type="button"
        title="Today: {tokenSplitLabel($todayTokens)} — click to switch units">
        {#if $costUnit === 'tokens'}
            Today: <span class="text-text">{formatTokens($todayTokens.total)}</span> tok
            <span class="opacity-60">({fmtCost($todayCost)})</span>
        {:else}
            Today: <span class="text-text">{fmtCost($todayCost)}</span>
            <span class="opacity-60">({formatTokens($todayTokens.total)} tok)</span>
        {/if}
    </button>

    {#if rlUtil !== undefined}
        <span title="Global rate-limit utilization">
            RL: <span class={rlUtil >= 0.85 ? 'text-status-ratelimit' : 'text-text'}>{fmtUtil(rlUtil)}</span>
        </span>
    {/if}

    <span class="ml-auto">
        Uptime: <span class="text-text">{uptimeStr}</span>
    </span>
</footer>
