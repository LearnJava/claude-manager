<script lang="ts">
    import { tick, beforeUpdate, afterUpdate, onMount } from 'svelte';
    import { sessionLogs, type LogEntry } from '../stores/sessions';
    import { logSearch, logSearchText, logSearchFocus } from '../stores/logSearch';
    import { logMarkdown } from '../stores/logView';
    import {
        hasMarkdown,
        hasStrongMarkdown,
        looksLikeMachineOutput,
        renderMarkdown,
    } from '../lib/markdown';
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
    // Highest LogEntry.seq seen in the previous render, NOT entries.length:
    // the log is a ring buffer (LOG_BUFFER_LIMIT in stores/sessions.ts) that
    // evicts the oldest entry for every new one once full, so length stops
    // growing forever after the cap — a length-based "did it grow" check goes
    // permanently false there and autoscroll silently dies for any session
    // whose log passes the cap. seq keeps climbing regardless of eviction.
    let prevLastSeq = 0;
    // Whether we were pinned to the bottom right before this update touched
    // the DOM. Computed in beforeUpdate (real scrollTop, pre-update) and
    // consumed in afterUpdate — not derived from watching for the 'scroll'
    // event our own pinToBottom() write triggers. That event-matching
    // approach (a one-shot "ignore next scroll" flag) is inherently racy
    // under a burst of fast-arriving log lines: a programmatic write that
    // happens to be a no-op fires no event at all, and a coalesced or
    // out-of-order event can consume the flag at the wrong time — either
    // way `stuckToBottom` can get stuck `false` with no way to self-correct
    // short of clicking "Jump to latest". Reading the DOM directly before
    // each update sidesteps that race entirely.
    let pendingStick = true;

    function distanceFromBottom(): number {
        if (!container) return 0;
        return container.scrollHeight - container.scrollTop - container.clientHeight;
    }

    function pinToBottom() {
        if (!container) return;
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

    // Levels whose message is prose that Claude (or the user) wrote, so any
    // markup in it is intentional and a single signal is enough.
    const MARKDOWN_LEVELS = new Set(['', 'text', 'thinking', 'result', 'user']);

    // Everything else — tool output above all — is formatted too, but only on
    // structural markup (`hasStrongMarkdown`) and never when the body is
    // machine output whose column alignment carries the meaning (a Read
    // listing with line numbers, a diff, a git listing). Those two guards are
    // the difference between formatting a .md file someone `cat`-ed and
    // mangling `cargo build` output.
    function autoMarkdown(e: LogEntry): boolean {
        const msg = e.message ?? '';
        if (MARKDOWN_LEVELS.has((e.level ?? '').toLowerCase())) return hasMarkdown(msg);
        return hasStrongMarkdown(msg) && !looksLikeMachineOutput(msg);
    }

    // Per-entry override of that decision, keyed by seq. The `M` button writes
    // here, so one row can be forced back to raw (a `## main` line from
    // `git status` really isn't a heading) or forced into markdown (output the
    // guards above held back). Nothing is remembered across restarts — this is
    // a per-glance decision, unlike the global Markdown checkbox.
    let mdOverride = new Map<number, boolean>();

    // The button is only offered where the call is genuinely ambiguous: tool
    // output and other non-prose levels that carry markup. Prose levels follow
    // the global checkbox alone, so their rows stay uncluttered.
    function offersMarkdown(e: LogEntry): boolean {
        if (MARKDOWN_LEVELS.has((e.level ?? '').toLowerCase())) return false;
        return hasMarkdown(e.message ?? '');
    }

    // `on` / `open` are the row's state *before* the click. Collapse state is
    // pinned here on purpose: it otherwise follows the markdown decision, so
    // switching a row back to raw would fold it to its first line — the click
    // would look like it hid the entry rather than unformatted it. Turning
    // rendering on always expands, since a collapsed body shows no formatting.
    function toggleForced(key: number, on: boolean, open: boolean) {
        mdOverride.set(key, !on);
        overrides.set(key, on ? open : true);
        mdOverride = mdOverride; // trigger Svelte reactivity
        overrides = overrides;
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
        stuckToBottom = distanceFromBottom() < FREEZE_THRESHOLD_PX;
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

    beforeUpdate(() => {
        pendingStick = distanceFromBottom() < FREEZE_THRESHOLD_PX;
    });

    afterUpdate(() => {
        if (!container) return;
        const lastEntry = entries[entries.length - 1];
        const lastSeq = lastEntry ? entryKey(lastEntry, entries.length - 1) : 0;
        const grew = lastSeq > prevLastSeq;
        prevLastSeq = lastSeq;
        if (grew && pendingStick) {
            pinToBottom();
            stuckToBottom = true;
        }
    });

    // Reset autoscroll state ONLY when the selected session actually changes.
    // Guarding on a remembered id is essential: this block re-evaluates on every
    // reactive update, and re-running its body each time would reset prevLastSeq
    // to 0, which makes afterUpdate perpetually detect "grew" and re-scroll on
    // every render — an infinite render loop that hard-freezes the page. The id
    // guard makes the body run once per session.
    // `overrides` is intentionally NOT reset here: seq keys are globally unique,
    // so entries a user opened in another session stay open when switching back.
    let lastSessionId: string | undefined;
    $: if (sessionId !== lastSessionId) {
        lastSessionId = sessionId;
        stuckToBottom = true;
        prevLastSeq = 0;
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
                {@const mdSrc = mdOverride.get(key) ?? autoMarkdown(e)}
                {@const md = $logMarkdown && mdSrc}
                {@const offered = $logMarkdown && offersMarkdown(e)}
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
                    {#if offered}
                        <button
                            type="button"
                            on:click={() => toggleForced(key, mdSrc, isOpen)}
                            title={mdSrc
                                ? 'Show this entry as raw text'
                                : 'Render this entry as markdown'}
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
        background: rgb(var(--c-bg-elevated));
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
    /* Link blue is set per theme: #60a5fa is a dark-theme tint and drops to
       ~3:1 contrast on the light panel. */
    .md-body :global(a) {
        color: #1d4ed8;
        text-decoration: underline;
    }
    .md-body :global(a:hover) { color: #2563eb; }
    :global(html.dark) .md-body :global(a) { color: #60a5fa; }
    :global(html.dark) .md-body :global(a:hover) { color: #93c5fd; }
</style>
