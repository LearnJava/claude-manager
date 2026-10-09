<script lang="ts">
    // One log entry's row — the exact rendering LogStream.svelte used to
    // inline in its {#each}. Pulled into its own component so both the
    // classic view and an expanded feed group (VIEW-TASKS.md UI-02) render
    // identically instead of duplicating the collapse/markdown logic.
    import { t } from '../lib/i18n';
    import type { LogEntry } from '../stores/sessions';
    import { logMarkdown } from '../stores/logView';
    import {
        hasMarkdown,
        hasStrongMarkdown,
        looksLikeMachineOutput,
        renderMarkdown,
    } from '../lib/markdown';
    import { formatTime, logEntryColor, logEntryIcon } from '../lib/formatters';
    import { copyText, COPY_FEEDBACK_MS } from '../lib/clipboard';
    import { codeCopy } from '../lib/codeCopy';
    import { foldDoneMarkers } from '../lib/askUserMarker';
    import { onDestroy } from 'svelte';

    export let entry: LogEntry;
    // Stable key for this row (seq, or a synthesized negative fallback) —
    // caller passes entryKey(entry, index) so both views agree.
    export let entryKey: number;
    // Hide the leading [hh:mm:ss] column — the feed view puts time in a
    // block-level title instead (UI-03), the classic view keeps it inline.
    export let showTime: boolean = true;

    const COLLAPSE_CHARS = 200;

    // Levels whose message is prose someone wrote, so a single markup signal
    // is enough (mirrors LogStream's original MARKDOWN_LEVELS).
    const MARKDOWN_LEVELS = new Set(['', 'text', 'thinking', 'result', 'user']);

    function autoMarkdown(e: LogEntry): boolean {
        const msg = e.message ?? '';
        if (MARKDOWN_LEVELS.has((e.level ?? '').toLowerCase())) return hasMarkdown(msg);
        return hasStrongMarkdown(msg) && !looksLikeMachineOutput(msg);
    }

    function offersMarkdown(e: LogEntry): boolean {
        if (MARKDOWN_LEVELS.has((e.level ?? '').toLowerCase())) return false;
        return hasMarkdown(e.message ?? '');
    }

    function isCollapsible(msg: string): boolean {
        if (!msg) return false;
        return msg.includes('\n') || msg.length > COLLAPSE_CHARS;
    }

    function summarize(msg: string): string {
        const nl = msg.indexOf('\n');
        let head = nl >= 0 ? msg.slice(0, nl) : msg;
        if (head.length > COLLAPSE_CHARS) head = head.slice(0, COLLAPSE_CHARS);
        return head + ' …';
    }

    // Per-row state — one component instance per entryKey, so this replaces
    // the seq-keyed Maps the monolithic LogStream used for the same purpose.
    let mdForced: boolean | null = null; // null = follow auto-detection
    let openForced: boolean | null = null; // null = follow default (mdSrc)

    function toggleForced(on: boolean, open: boolean) {
        mdForced = !on;
        openForced = on ? open : true;
    }

    function toggle(open: boolean) {
        openForced = !open;
    }

    let mdCacheSrc = '';
    let mdCacheHtml = '';
    function renderCached(msg: string): string {
        if (mdCacheSrc === msg) return mdCacheHtml;
        mdCacheHtml = renderMarkdown(msg);
        mdCacheSrc = msg;
        return mdCacheHtml;
    }

    // Hover copy button: always the full message source (markdown as
    // written), even when the row is collapsed to its summary.
    let copyState: 'idle' | 'ok' | 'fail' = 'idle';
    let copyTimer: ReturnType<typeof setTimeout> | undefined;

    async function copyEntry() {
        copyState = (await copyText(msg)) ? 'ok' : 'fail';
        clearTimeout(copyTimer);
        copyTimer = setTimeout(() => (copyState = 'idle'), COPY_FEEDBACK_MS);
    }

    onDestroy(() => clearTimeout(copyTimer));

    // Full source (copy button) vs. what the row displays: the
    // end-of-session "done" marker block is folded into one line
    // (lib/askUserMarker.ts) — it is not a question, nothing answers it.
    $: msg = entry.message ?? '';
    $: shown = foldDoneMarkers(msg, (s) =>
        '✓ ' + (s ? $t('logStream.doneMarkerWith', { s }) : $t('logStream.doneMarker')),
    );
    $: mdSrc = mdForced ?? autoMarkdown(entry);
    $: md = $logMarkdown && mdSrc;
    $: offered = $logMarkdown && offersMarkdown(entry);
    $: collapsible = isCollapsible(shown);
    $: isOpen = collapsible ? openForced ?? mdSrc : true;
    $: html = md && isOpen ? renderCached(shown) : '';
</script>

<div class="group/row relative flex items-start gap-2 py-px {logEntryColor(entry)}">
    {#if showTime}
        <span class="text-text-dim shrink-0 select-none">
            [{formatTime(entry.time)}]
        </span>
    {/if}
    <span class="shrink-0 select-none w-4 text-center">
        {logEntryIcon(entry)}
    </span>
    {#if collapsible}
        <button
            type="button"
            on:click={() => toggle(isOpen)}
            title={isOpen ? $t('logStream.collapse') : $t('logStream.expand')}
            class="shrink-0 select-none w-4 text-center text-text-dim
                   hover:text-text font-bold leading-5">
            {isOpen ? '−' : '＋'}
        </button>
    {:else}
        <span class="shrink-0 w-4 select-none"></span>
    {/if}
    {#if offered}
        <button
            type="button"
            on:click={() => toggleForced(mdSrc, isOpen)}
            title={mdSrc ? $t('logStream.showAsRaw') : $t('logStream.renderAsMarkdown')}
            class="shrink-0 select-none w-4 text-center leading-5 text-[10px]
                   rounded {mdSrc
                       ? 'text-blue-600 dark:text-blue-400 font-bold'
                       : 'text-text-dim hover:text-text'}">
            M
        </button>
    {:else}
        <span class="shrink-0 w-4 select-none"></span>
    {/if}
    {#if md && isOpen}
        <div
            class="md-body min-w-0 flex-1 break-words"
            use:codeCopy={{
                html,
                label: $t('logStream.copyCode'),
                copied: $t('logStream.copied'),
                failed: $t('logStream.copyFailed'),
            }}>
            {@html html}
        </div>
    {:else}
        <span class="whitespace-pre-wrap break-words">
            {collapsible && !isOpen ? summarize(shown) : shown}
        </span>
    {/if}
    {#if msg}
        <button
            type="button"
            data-testid="entry-copy"
            on:click={copyEntry}
            title={copyState === 'ok'
                ? $t('logStream.copied')
                : copyState === 'fail'
                  ? $t('logStream.copyFailed')
                  : $t('logStream.copyEntry')}
            class="absolute top-0 right-0 select-none p-0.5 rounded border border-bg-border
                   bg-bg-panel shadow-sm group-hover/row:visible focus-visible:visible
                   {copyState === 'idle' ? 'invisible text-text-dim hover:text-text' : ''}
                   {copyState === 'ok' ? 'text-green-600 dark:text-status-working' : ''}
                   {copyState === 'fail' ? 'text-red-600 dark:text-status-error' : ''}">
            {#if copyState === 'ok'}
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                     stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
            {:else if copyState === 'fail'}
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                     stroke-width="2.5" stroke-linecap="round"><path d="M18 6 6 18M6 6l12 12" /></svg>
            {:else}
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                     stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></svg>
            {/if}
        </button>
    {/if}
</div>
