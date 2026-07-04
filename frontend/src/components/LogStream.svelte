<script lang="ts">
    import { tick, afterUpdate, onMount } from 'svelte';
    import { sessionLogs, type LogEntry } from '../stores/sessions';
    import { logSearch, logSearchText, logSearchFocus } from '../stores/logSearch';
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

    // Entries longer than this (or multi-line) are collapsed to their first line
    // behind a ＋/− toggle so verbose tool output / prompts don't flood the view.
    const COLLAPSE_CHARS = 200;
    // Indices (into `entries`) the user has explicitly expanded.
    let expanded = new Set<number>();

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

    function toggle(i: number) {
        if (expanded.has(i)) expanded.delete(i);
        else expanded.add(i);
        expanded = expanded; // trigger Svelte reactivity
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
        const distance =
            container.scrollHeight - container.scrollTop - container.clientHeight;
        stuckToBottom = distance < FREEZE_THRESHOLD_PX;
    }

    async function jumpToBottom() {
        if (!container) return;
        await tick();
        container.scrollTop = container.scrollHeight;
        stuckToBottom = true;
    }

    onMount(() => {
        // Start pinned to bottom on first mount.
        if (container) container.scrollTop = container.scrollHeight;
    });

    afterUpdate(() => {
        if (!container) return;
        const grew = entries.length > prevLogLen;
        prevLogLen = entries.length;
        if (grew && stuckToBottom) {
            container.scrollTop = container.scrollHeight;
        }
    });

    // Reset autoscroll state ONLY when the selected session actually changes.
    // Guarding on a remembered id is essential: this block re-evaluates on every
    // reactive update, and re-running its body each time resets prevLogLen to 0,
    // which makes afterUpdate perpetually detect "grew" and re-scroll, whose
    // scroll event re-invalidates stuckToBottom — an infinite render loop that
    // hard-freezes the page. The id guard makes the body run once per session.
    let lastSessionId: string | undefined;
    $: if (sessionId !== lastSessionId) {
        lastSessionId = sessionId;
        stuckToBottom = true;
        prevLogLen = 0;
        expanded = new Set();
        // Wait until the new entries are rendered, then pin to bottom.
        tick().then(() => {
            if (container) container.scrollTop = container.scrollHeight;
        });
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
            {#each entries as e, i (i)}
                {@const msg = e.message ?? ''}
                {@const collapsible = isCollapsible(msg)}
                {@const isOpen = expanded.has(i)}
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
                            on:click={() => toggle(i)}
                            title={isOpen ? 'Collapse' : 'Expand'}
                            class="shrink-0 select-none w-4 text-center text-text-dim
                                   hover:text-text font-bold leading-5">
                            {isOpen ? '−' : '＋'}
                        </button>
                    {:else}
                        <span class="shrink-0 w-4 select-none"></span>
                    {/if}
                    <span class="whitespace-pre-wrap break-words">
                        {collapsible && !isOpen ? summarize(msg) : msg}
                    </span>
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
