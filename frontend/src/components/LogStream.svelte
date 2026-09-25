<script lang="ts">
    import { tick, beforeUpdate, afterUpdate, onMount } from 'svelte';
    import { sessionLogs, type LogEntry } from '../stores/sessions';
    import { logSearch, logSearchText, logSearchFocus } from '../stores/logSearch';
    import { logMarkdown, logLayout } from '../stores/logView';
    import { t } from '../lib/i18n';
    import { groupEntries, entryKey } from '../lib/logGroups';
    import { formatTime } from '../lib/formatters';
    import LogEntryRow from './LogEntryRow.svelte';
    import ToolCallRow from './ToolCallRow.svelte';

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
    // behind a ＋/− toggle — used by the feed view's group header ellipsis and
    // read-only summary; per-row collapse now lives in LogEntryRow.svelte.
    const COLLAPSE_CHARS = 200;

    // Feed-view group expand state, keyed by the group's seq (its first
    // entry's LogEntry.seq) — same reasoning as LogStream's old row
    // `overrides`: indices shift under the ring buffer and search filter,
    // seq does not. Absent from the map = collapsed by default.
    let groupOpen = new Map<number, boolean>();

    function toggleGroup(seq: number, open: boolean) {
        groupOpen.set(seq, !open);
        groupOpen = groupOpen; // trigger Svelte reactivity
    }

    function onLayoutToggle(e: Event) {
        logLayout.set((e.currentTarget as HTMLInputElement).checked ? 'feed' : 'classic');
    }

    $: allEntries = ($sessionLogs[sessionId] ?? []) as LogEntry[];
    $: filter = ($logSearchText ?? '').toLowerCase();
    $: entries = filter
        ? allEntries.filter((e) => entryMatches(e, filter))
        : allEntries;
    // Feed view groups the *filtered* entries, same as the classic view
    // filters rows — a group whose only surviving member matched the search
    // still shows correctly grouped instead of leaking raw entries around it.
    $: blocks = groupEntries(entries);

    // A block matches the search if any of its entries do — used to
    // auto-expand the group containing a hit (UI-02 "Группа … раскрывается").
    $: matchingSeqs = filter
        ? new Set(blocks.filter((b) => b.entries.some((e) => entryMatches(e, filter))).map((b) => b.seq))
        : new Set<number>();

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
            placeholder={$t('logStream.filterPlaceholder')}
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
                title={$t('logStream.clearFilterTitle')}
                class="text-text-muted hover:text-text text-xs px-1">✕</button>
            <span class="text-text-muted text-[11px] select-none">
                {entries.length}/{allEntries.length}
            </span>
        {/if}
        <label
            title={$t('logStream.renderMarkdownTitle')}
            class="flex items-center gap-1 shrink-0 text-[11px] text-text-muted
                   select-none cursor-pointer whitespace-nowrap">
            <input
                type="checkbox"
                bind:checked={$logMarkdown}
                class="w-3 h-3 accent-blue-500 cursor-pointer" />
            {$t('logStream.markdownLabel')}
        </label>
        <label
            title={$t('logStream.feedLayoutTitle')}
            class="flex items-center gap-1 shrink-0 text-[11px] text-text-muted
                   select-none cursor-pointer whitespace-nowrap">
            <input
                type="checkbox"
                checked={$logLayout === 'feed'}
                on:change={onLayoutToggle}
                class="w-3 h-3 accent-blue-500 cursor-pointer" />
            {$t('logStream.feedLayoutLabel')}
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
                    {$t('logStream.noEntriesMatch')} <code>{filter}</code>.
                {:else}
                    {$t('logStream.noLogEntries')}
                {/if}
            </div>
        {:else if $logLayout === 'classic'}
            {#each entries as e, i (entryKey(e, i))}
                <LogEntryRow entry={e} entryKey={entryKey(e, i)} />
            {/each}
        {:else}
            {#each blocks as block (block.seq)}
                {#if block.kind === 'tools'}
                    {@const isOpen = groupOpen.get(block.seq) ?? matchingSeqs.has(block.seq)}
                    <div class="py-px">
                        <button
                            type="button"
                            on:click={() => toggleGroup(block.seq, isOpen)}
                            class="flex items-center gap-2 w-full text-left text-sky-700
                                   dark:text-sky-400 hover:text-text">
                            <span class="shrink-0 select-none w-4 text-center text-text-dim
                                         font-bold leading-5">
                                {isOpen ? '−' : '＋'}
                            </span>
                            <span class="shrink-0 select-none">{block.summary?.firstEmoji ?? '🔧'}</span>
                            {#if block.summary?.readOnly}
                                <span class="truncate">
                                    {$t('logStream.readOnlySummary')}: {block.summary.callLabels.join(', ')}
                                </span>
                            {:else}
                                <span class="truncate">{block.summary?.firstLabel}</span>
                                {#if block.summary && block.summary.extraCount > 0}
                                    <span class="text-text-dim shrink-0">
                                        + {block.summary.extraCount} {$t('logStream.moreCommands')}
                                    </span>
                                {/if}
                            {/if}
                            {#if block.summary?.hasError}
                                <span class="text-red-600 dark:text-status-error shrink-0 font-bold">
                                    ✖
                                </span>
                            {/if}
                            {#if !isOpen && block.summary?.toolCalls.some((c) => c.state === 'running')}
                                <span
                                    data-testid="group-spinner"
                                    class="shrink-0 inline-block w-3 h-3 rounded-full border-2
                                           border-text-dim border-t-transparent animate-spin"></span>
                            {/if}
                        </button>
                        {#if isOpen}
                            {@const calls = new Map((block.summary?.toolCalls ?? []).map((c) => [c.call, c]))}
                            {@const results = new Set((block.summary?.toolCalls ?? []).map((c) => c.result))}
                            <div class="pl-6 border-l border-bg-border ml-2">
                                {#each block.entries as e, i (entryKey(e, i))}
                                    {#if calls.has(e)}
                                        {@const tc = calls.get(e)}
                                        <ToolCallRow
                                            {tc}
                                            forceOpen={!!filter &&
                                                (entryMatches(tc.call, filter) ||
                                                    (!!tc.result && entryMatches(tc.result, filter)))} />
                                    {:else if !results.has(e)}
                                        <LogEntryRow entry={e} entryKey={entryKey(e, i)} />
                                    {/if}
                                {/each}
                            </div>
                        {/if}
                    </div>
                {:else if block.kind === 'edit'}
                    {@const isOpen = groupOpen.get(block.seq) ?? false}
                    {@const summary = block.editSummary}
                    <div class="py-px">
                        <button
                            type="button"
                            on:click={() => toggleGroup(block.seq, isOpen)}
                            title={summary?.filePath}
                            class="flex items-center gap-2 w-full text-left text-orange-700
                                   dark:text-status-waiting hover:text-text">
                            <span class="shrink-0 select-none w-4 text-center text-text-dim
                                         font-bold leading-5">
                                {isOpen ? '−' : '＋'}
                            </span>
                            <span class="shrink-0 select-none">✎</span>
                            <span class="truncate">{summary?.fileName}</span>
                            {#if summary && summary.added > 0}
                                <span class="text-green-700 dark:text-status-working shrink-0">
                                    +{summary.added}
                                </span>
                            {/if}
                            {#if summary && summary.removed > 0}
                                <span class="text-red-600 dark:text-status-error shrink-0">
                                    −{summary.removed}
                                </span>
                            {/if}
                        </button>
                        {#if isOpen}
                            <div class="pl-6 border-l border-bg-border ml-2">
                                <div class="font-mono text-[12px] leading-5 whitespace-pre-wrap break-words">
                                    {#each summary?.lines ?? [] as line}
                                        <div
                                            class={line.type === 'add'
                                                ? 'bg-green-50 dark:bg-green-950 text-green-800 dark:text-status-working'
                                                : 'bg-red-50 dark:bg-red-950 text-red-700 dark:text-status-error'}>
                                            {line.type === 'add' ? '+' : '−'}{line.text}
                                        </div>
                                    {/each}
                                    {#if summary && summary.truncated > 0}
                                        <div class="text-text-dim italic">
                                            … {$t('logStream.editMoreLines', { n: summary.truncated })}
                                        </div>
                                    {/if}
                                </div>
                                {#each block.entries.slice(1) as e, i (entryKey(e, i + 1))}
                                    <LogEntryRow entry={e} entryKey={entryKey(e, i + 1)} />
                                {/each}
                            </div>
                        {/if}
                    </div>
                {:else if block.kind === 'user'}
                    <div class="flex justify-end py-1">
                        <div class="max-w-[80%] bg-bg-elevated rounded px-3 py-1.5">
                            <LogEntryRow entry={block.entries[0]} entryKey={block.seq} showTime={false} />
                        </div>
                    </div>
                {:else if block.kind === 'thinking'}
                    {@const isOpen = groupOpen.get(block.seq) ?? false}
                    {@const label =
                        block.thinkingSeconds === null || block.thinkingSeconds === undefined
                            ? $t('logStream.thinkingNow')
                            : $t('logStream.thoughtFor', { s: block.thinkingSeconds })}
                    <div class="py-px" title={formatTime(block.entries[0].time)}>
                        <button
                            type="button"
                            on:click={() => toggleGroup(block.seq, isOpen)}
                            class="flex items-center gap-2 w-full text-left text-text-dim
                                   italic hover:text-text">
                            <span class="shrink-0 select-none w-4 text-center font-bold leading-5">
                                {isOpen ? '−' : '＋'}
                            </span>
                            <span class="truncate">{label}</span>
                        </button>
                        {#if isOpen}
                            <div class="pl-6 border-l border-bg-border ml-2">
                                <LogEntryRow entry={block.entries[0]} entryKey={block.seq} showTime={false} />
                            </div>
                        {/if}
                    </div>
                {:else if block.kind === 'prose'}
                    <div
                        class="font-sans text-[13px] leading-[1.5] max-w-[80ch] py-1"
                        title={formatTime(block.entries[0].time)}>
                        <LogEntryRow entry={block.entries[0]} entryKey={block.seq} showTime={false} />
                    </div>
                {:else}
                    <div title={formatTime(block.entries[0].time)}>
                        <LogEntryRow entry={block.entries[0]} entryKey={block.seq} showTime={false} />
                    </div>
                {/if}
            {/each}
        {/if}
    </div>

    {#if !stuckToBottom && entries.length > 0}
        <button
            type="button"
            on:click={jumpToBottom}
            class="absolute bottom-2 right-4 bg-bg-elevated border border-bg-border
                   text-text text-xs px-2 py-1 rounded shadow hover:bg-bg-panel">
            ↓ {$t('logStream.jumpToLatest')}
        </button>
    {/if}
</div>
