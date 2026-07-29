<script lang="ts">
    import { tick, afterUpdate, onMount } from 'svelte';
    import { sessionLogs, type LogEntry } from '../stores/sessions';
    import { logSearch, logSearchText, logSearchFocus } from '../stores/logSearch';
    import { logMarkdown } from '../stores/logView';
    import { hasMarkdown, renderMarkdown } from '../lib/markdown';
    import {
        formatTime,
        logEntryColor,
        logEntryIcon,
    } from '../lib/formatters';

    export let sessionId: string;
    // Threshold (px) — if the user has scrolled further than this from the
    // bottom, we freeze autoscroll until they return.
    const FREEZE_THRESHOLD_PX = 60;

    let container: HTMLDivElement | undefined;
    let searchInput: HTMLInputElement | undefined;
    let stuckToBottom = true;
    let prevLogLen = 0;
    // Programmatic scrollTop writes (pin-to-bottom) still emit a 'scroll'
    // event, sometimes before the browser has finished laying out a burst of
    // newly-appended entries — onScroll can then read a stale scrollHeight
    // and mistake our own auto-scroll for the user scrolling away, freezing
    // autoscroll with no way back short of clicking "Jump to latest". Ignore
    // exactly the one 'scroll' event that follows our own write.
    let ignoreNextScroll = false;

    function pinToBottom() {
        if (!container) return;
        ignoreNextScroll = true;
        container.scrollTop = container.scrollHeight;
    }

    // Entries longer than this (or multi-line) are collapsed to their first line
    // behind a ＋/− toggle so verbose tool output / prompts don't flood the view.
    const COLLAPSE_CHARS = 200;
    // Explicit open/closed state, keyed by seq (LogEntry.seq), not by array
    // index: indices shift when the ring buffer trims old entries or the search
    // filter changes, which would silently expand the wrong rows. Only rows the
    // user actually clicked are in here; everything else follows the default
    // below (markdown bodies default to open — a collapsed heading, table or
    // code block is exactly the part that needed formatting).
    let overrides = new Map<number, boolean>();

    // Levels whose message is prose that Claude (or the user) wrote, so markup
    // in it is intentional. Tool input/output, errors and cost lines are left
    // raw: a `# comment` line in a shell transcript is not a heading.
    const MARKDOWN_LEVELS = new Set(['', 'text', 'thinking', 'result', 'user']);

    // Deliberately independent of the Markdown checkbox: it decides whether an
    // entry is *authored* as markdown, which is what drives the default
    // expanded state. Flipping the checkbox then only changes the presentation
    // — it never collapses or expands rows under the user.
    function isMarkdownSource(e: LogEntry): boolean {
        if (!MARKDOWN_LEVELS.has((e.level ?? '').toLowerCase())) return false;
        return hasMarkdown(e.message ?? '');
    }

    // renderMarkdown re-runs for every visible row whenever any reactive
    // dependency changes (an expand click, a filter keystroke, a new entry).
    // Memoize per seq so a long log isn't re-parsed on every one of them.
    const mdCache = new Map<number, { src: string; html: string }>();

    function renderCached(key: number, msg: string): string {
        const hit = mdCache.get(key);
        if (hit && hit.src === msg) return hit.html;
        const html = renderMarkdown(msg);
        if (mdCache.size > 1000) mdCache.clear();
        mdCache.set(key, { src: msg, html });
        return html;
    }

    function isCollapsible(msg: string): boolean {
        if (!msg) return false;
        return msg.includes('\n') || msg.length > COLLAPSE_CHARS;
    }

    // First line, capped at COLLAPSE_CHARS, with an ellipsis marking hidden rest.
    function summarize(msg: string): string {
        const nl = msg.indexOf('\n');
        let head = nl >= 0 ? msg.slice(0, nl) : msg;
        if (head.length > COLLAPSE_CHARS) head = head.slice(0, COLLAPSE_CHARS);
        return head + ' …';
    }

    // Fallback key for entries without a seq (should not happen for live
    // entries; negative range avoids colliding with real seq values).
    function entryKey(e: LogEntry, i: number): number {
        return e.seq ?? -(i + 1);
    }

    function toggle(key: number, open: boolean) {
        overrides.set(key, !open);
        overrides = overrides; // trigger Svelte reactivity
    }

    $: allEntries = ($sessionLogs[sessionId] ?? []) as LogEntry[];
    $: filter = ($logSearchText ?? '').toLowerCase();
    $: entries = filter
        ? allEntries.filter((e) => entryMatches(e, filter))
        : allEntries;

    function entryMatches(e: LogEntry, q: string): boolean {
        if (!q) return true;
        const hay =
            (e.message ?? '') +
            ' ' +
            (e.tool_name ?? '') +
            ' ' +
            (e.tool_input ?? '') +
            ' ' +
            (e.level ?? '') +
            ' ' +
            (e.source ?? '');
        return hay.toLowerCase().includes(q);
    }

    function onScroll() {
        if (!container) return;
        if (ignoreNextScroll) {
            ignoreNextScroll = false;
            return;
        }
        const distance =
            container.scrollHeight - container.scrollTop - container.clientHeight;
        stuckToBottom = distance < FREEZE_THRESHOLD_PX;
    }

    async function jumpToBottom() {
        if (!container) return;
        await tick();
        pinToBottom();
        stuckToBottom = true;
    }

    onMount(() => {
        // Start pinned to bottom on first mount.
        pinToBottom();
    });

    afterUpdate(() => {
        if (!container) return;
        const grew = entries.length > prevLogLen;
        prevLogLen = entries.length;
        if (grew && stuckToBottom) {
            pinToBottom();
        }
    });

    // Reset autoscroll state ONLY when the selected session actually changes.
    // Guarding on a remembered id is essential: this block re-evaluates on every
    // reactive update, and re-running its body each time resets prevLogLen to 0,
    // which makes afterUpdate perpetually detect "grew" and re-scroll, whose
    // scroll event re-invalidates stuckToBottom — an infinite render loop that
    // hard-freezes the page. The id guard makes the body run once per session.
    // `overrides` is intentionally NOT reset here: seq keys are globally unique,
    // so entries a user opened in another session stay open when switching back.
    let lastSessionId: string | undefined;
    $: if (sessionId !== lastSessionId) {
        lastSessionId = sessionId;
        stuckToBottom = true;
        prevLogLen = 0;
        // Wait until the new entries are rendered, then pin to bottom.
        tick().then(() => pinToBottom());
    }

    // Focus the search box when Ctrl+F is triggered in App.svelte.
    $: if ($logSearchFocus > 0 && searchInput) {
        searchInput.focus();
        searchInput.select();
    }

    function onSearchInput(e: Event) {
        logSearch.setText((e.currentTarget as HTMLInputElement).value);
    }

    function clearSearch() {
        logSearch.clear();
        searchInput?.focus();
    }

    function onSearchKey(e: KeyboardEvent) {
        if (e.key === 'Escape') {
            clearSearch();
        }
    }
</script>

<div class="relative flex-1 min-h-0 flex flex-col">
    <!-- Search bar — Ctrl+F focuses, Esc clears. -->
    <div class="flex items-center gap-2 px-2 py-1 border-b border-bg-border bg-bg-panel">
        <span class="text-text-muted text-xs select-none">🔍</span>
        <input
            bind:this={searchInput}
            type="search"
            placeholder="Filter log… (Ctrl+F)"
            value={$logSearchText}
            on:input={onSearchInput}
            on:keydown={onSearchKey}
            class="flex-1 bg-bg border border-bg-border rounded px-2 py-0.5 text-xs text-text
                   placeholder:text-text-dim focus:outline-none focus:border-blue-500"
        />
        {#if $logSearchText}
            <button
                type="button"
                on:click={clearSearch}
                title="Clear filter (Esc)"
                class="text-text-muted hover:text-text text-xs px-1">✕</button>
            <span class="text-text-muted text-[11px] select-none">
                {entries.length}/{allEntries.length}
            </span>
        {/if}
        <label
            title="Render markdown (headings, lists, tables, code) instead of raw text"
            class="flex items-center gap-1 shrink-0 text-[11px] text-text-muted
                   select-none cursor-pointer whitespace-nowrap">
            <input
                type="checkbox"
                bind:checked={$logMarkdown}
                class="w-3 h-3 accent-blue-500 cursor-pointer" />
            Markdown
        </label>
    </div>

    <div
        bind:this={container}
        on:scroll={onScroll}
        class="flex-1 min-h-0 overflow-y-auto font-mono text-[12px] leading-5
               bg-bg px-3 py-2 select-text">
        {#if entries.length === 0}
            <div class="text-text-dim italic py-2">
                {#if filter}
                    No entries match <code>{filter}</code>.
                {:else}
                    No log entries yet.
                {/if}
            </div>
        {:else}
            {#each entries as e, i (entryKey(e, i))}
                {@const msg = e.message ?? ''}
                {@const key = entryKey(e, i)}
                {@const mdSrc = isMarkdownSource(e)}
                {@const md = $logMarkdown && mdSrc}
                {@const collapsible = isCollapsible(msg)}
                {@const isOpen = collapsible ? overrides.get(key) ?? mdSrc : true}
                <div class="flex items-start gap-2 py-px {logEntryColor(e)}">
                    <span class="text-text-dim shrink-0 select-none">
                        [{formatTime(e.time)}]
                    </span>
                    <span class="shrink-0 select-none w-4 text-center">
                        {logEntryIcon(e)}
                    </span>
                    {#if collapsible}
                        <button
                            type="button"
                            on:click={() => toggle(key, isOpen)}
                            title={isOpen ? 'Collapse' : 'Expand'}
                            class="shrink-0 select-none w-4 text-center text-text-dim
                                   hover:text-text font-bold leading-5">
                            {isOpen ? '−' : '＋'}
                        </button>
                    {:else}
                        <span class="shrink-0 w-4 select-none"></span>
                    {/if}
                    {#if md && isOpen}
                        <div class="md-body min-w-0 flex-1 break-words">
                            {@html renderCached(key, msg)}
                        </div>
                    {:else}
                        <span class="whitespace-pre-wrap break-words">
                            {collapsible && !isOpen ? summarize(msg) : msg}
                        </span>
                    {/if}
                </div>
            {/each}
        {/if}
    </div>

    {#if !stuckToBottom && entries.length > 0}
        <button
            type="button"
            on:click={jumpToBottom}
            class="absolute bottom-2 right-4 bg-bg-elevated border border-bg-border
                   text-text text-xs px-2 py-1 rounded shadow hover:bg-bg-panel">
            ↓ Jump to latest
        </button>
    {/if}
</div>

<!--
  Styling for {@html}-injected markdown. Svelte scopes component CSS by adding
  a class to elements it compiled, which injected nodes never get — hence
  :global() under the .md-body wrapper. Tailwind's preflight resets lists,
  headings and tables to nothing, so every element needs its rule back.
  Colors come from the theme CSS vars so light/dark both work.
-->
<style>
    .md-body {
        font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto,
            'Helvetica Neue', sans-serif;
        line-height: 1.5;
    }
    .md-body :global(p) {
        margin: 0.15em 0;
    }
    .md-body :global(h1),
    .md-body :global(h2),
    .md-body :global(h3),
    .md-body :global(h4),
    .md-body :global(h5),
    .md-body :global(h6) {
        font-weight: 600;
        line-height: 1.3;
        margin: 0.5em 0 0.2em;
        color: rgb(var(--c-text));
    }
    .md-body :global(h1) { font-size: 1.5em; }
    .md-body :global(h2) { font-size: 1.3em; }
    .md-body :global(h3) { font-size: 1.15em; }
    .md-body :global(h4),
    .md-body :global(h5),
    .md-body :global(h6) { font-size: 1em; }
    .md-body :global(strong) {
        font-weight: 600;
        color: rgb(var(--c-text));
    }
    .md-body :global(em) { font-style: italic; }
    .md-body :global(del) { text-decoration: line-through; opacity: 0.7; }
    .md-body :global(ul),
    .md-body :global(ol) {
        margin: 0.2em 0;
        padding-left: 1.5em;
    }
    .md-body :global(ul) { list-style: disc; }
    .md-body :global(ol) { list-style: decimal; }
    .md-body :global(li) { margin: 0.1em 0; }
    .md-body :global(li > ul),
    .md-body :global(li > ol) { margin: 0.1em 0; }
    .md-body :global(code) {
        font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        font-size: 0.92em;
        background: rgb(var(--c-bg-elevated));
        border-radius: 3px;
        padding: 0 0.25em;
    }
    .md-body :global(pre) {
        background: rgb(var(--c-bg-panel));
        border: 1px solid rgb(var(--c-bg-border));
        border-radius: 4px;
        padding: 0.4em 0.6em;
        margin: 0.3em 0;
        overflow-x: auto;
    }
    .md-body :global(pre code) {
        background: none;
        padding: 0;
        white-space: pre;
    }
    .md-body :global(blockquote) {
        border-left: 3px solid rgb(var(--c-bg-border));
        padding-left: 0.6em;
        margin: 0.3em 0;
        color: rgb(var(--c-text-dim));
    }
    .md-body :global(hr) {
        border: 0;
        border-top: 1px solid rgb(var(--c-bg-border));
        margin: 0.5em 0;
    }
    .md-body :global(table) {
        border-collapse: collapse;
        margin: 0.3em 0;
        font-size: 0.95em;
    }
    .md-body :global(th),
    .md-body :global(td) {
        border: 1px solid rgb(var(--c-bg-border));
        padding: 0.15em 0.45em;
        text-align: left;
        vertical-align: top;
    }
    .md-body :global(th) {
        background: rgb(var(--c-bg-elevated));
        font-weight: 600;
        color: rgb(var(--c-text));
    }
    .md-body :global(a) {
        color: #60a5fa;
        text-decoration: underline;
    }
    .md-body :global(a:hover) { color: #93c5fd; }
</style>
