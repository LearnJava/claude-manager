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

    $: msg = entry.message ?? '';
    $: mdSrc = mdForced ?? autoMarkdown(entry);
    $: md = $logMarkdown && mdSrc;
    $: offered = $logMarkdown && offersMarkdown(entry);
    $: collapsible = isCollapsible(msg);
    $: isOpen = collapsible ? openForced ?? mdSrc : true;
</script>

<div class="flex items-start gap-2 py-px {logEntryColor(entry)}">
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
        <div class="md-body min-w-0 flex-1 break-words">
            {@html renderCached(msg)}
        </div>
    {:else}
        <span class="whitespace-pre-wrap break-words">
            {collapsible && !isOpen ? summarize(msg) : msg}
        </span>
    {/if}
</div>
