<script lang="ts">
    import { onDestroy } from 'svelte';
    import type { ToolCall } from '../lib/logGroups';
    import { entryKey } from '../lib/logGroups';
    import { toolDisplay } from '../lib/toolDisplay';
    import { formatCallDuration } from '../lib/formatters';
    import { locale } from '../lib/i18n';
    import LogEntryRow from './LogEntryRow.svelte';

    export let tc: ToolCall;
    // A search hit inside this call opens the row (UI-11); a manual click wins.
    export let forceOpen = false;

    const ERROR_CHARS = 60;

    let override: boolean | undefined;
    $: open = override ?? forceOpen;

    $: d = toolDisplay(tc.call, $locale);
    $: running = tc.state === 'running';
    $: startMs = Date.parse(tc.call.time);

    let now = Date.now();
    let timer: ReturnType<typeof setInterval> | undefined;
    $: if (running && !timer) {
        now = Date.now();
        timer = setInterval(() => (now = Date.now()), 1000);
    } else if (!running && timer) {
        clearInterval(timer);
        timer = undefined;
    }
    onDestroy(() => timer && clearInterval(timer));

    $: elapsed = running
        ? formatCallDuration(isNaN(startMs) ? undefined : Math.max(0, now - startMs))
        : formatCallDuration(tc.durationMs);
    $: errorText = tc.state === 'error' ? (tc.result?.message ?? '').trim().replace(/\s+/g, ' ').slice(0, ERROR_CHARS) : '';
    $: members = tc.result ? [tc.call, tc.result] : [tc.call];
</script>

<div class="py-px" data-testid="tool-call-row" data-state={tc.state}>
    <button
        type="button"
        on:click={() => (override = !open)}
        class="flex items-center gap-2 w-full text-left text-sky-700 dark:text-sky-400 hover:text-text">
        <span class="shrink-0 select-none w-4 text-center text-text-dim font-bold leading-5">
            {open ? '−' : '＋'}
        </span>
        <span class="shrink-0 select-none">{d.emoji}</span>
        <span class="shrink-0">{d.verb}</span>
        <span class="truncate">{d.detail}</span>
        {#if elapsed}
            <span class="text-text-dim shrink-0">· {elapsed}</span>
        {/if}
        {#if running}
            <span
                data-testid="tool-spinner"
                class="shrink-0 inline-block w-3 h-3 rounded-full border-2 border-text-dim
                       border-t-transparent animate-spin"></span>
        {:else if tc.state === 'error'}
            <span class="shrink-0 text-red-600 dark:text-status-error font-bold">✖</span>
            {#if errorText}
                <span class="truncate text-red-600 dark:text-status-error">{errorText}</span>
            {/if}
        {:else}
            <span class="shrink-0 text-green-700 dark:text-status-working font-bold">✓</span>
        {/if}
    </button>
    {#if open}
        <div class="pl-6 border-l border-bg-border ml-2">
            {#each members as e, i (entryKey(e, i))}
                <LogEntryRow entry={e} entryKey={entryKey(e, i)} />
            {/each}
        </div>
    {/if}
</div>
