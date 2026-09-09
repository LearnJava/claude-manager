<script lang="ts">
    import { createEventDispatcher, onMount } from 'svelte';
    import { GetHistory, GetSessionLog } from '../../wailsjs/go/main/App';
    import { projects } from '../stores/projects';
    import {
        formatCost,
        formatDuration,
        formatTime,
        formatTokens,
        logEntryColor,
        logEntryIcon,
    } from '../lib/formatters';
    import { t } from '../lib/i18n';

    const dispatch = createEventDispatcher();

    // The Go store.SessionRun struct is exposed without JSON tags, so field
    // names arrive capitalized.
    interface SessionRun {
        ID: number;
        Project: string;
        Session: string;
        CLISessionID: string;
        Model: string;
        StartedAt: string;
        FinishedAt: string | null;
        Status: string;
        TasksDone: number;
        ExitCode: number | null;
        ErrorMsg: string;
        TotalCostUSD: number;
        InputTokens: number;
        OutputTokens: number;
        CacheReadTokens: number;
        CacheCreationTokens: number;
        NumTurns: number;
        DurationMs: number;
    }

    interface LogEntry {
        ID: number;
        RunID: number;
        Timestamp: string;
        Level: string;
        Message: string;
        ToolName: string;
        ToolInput: string;
    }

    type SortKey =
        | 'session'
        | 'started'
        | 'duration'
        | 'turns'
        | 'tasks'
        | 'cost'
        | 'status';

    let runs: SessionRun[] = [];
    let loading = true;
    let error = '';

    // Filters
    let filterProject = '';
    let filterSession = '';
    let filterStatus = '';
    let filterFrom = '';
    let filterTo = '';

    // Sorting
    let sortKey: SortKey = 'started';
    let sortDir: 'asc' | 'desc' = 'desc';

    // Row expansion
    let expandedRunID: number | null = null;
    let logsByRunID: Record<number, LogEntry[] | undefined> = {};
    let logsLoadingByRunID: Record<number, boolean> = {};
    let logsErrorByRunID: Record<number, string> = {};

    async function load() {
        loading = true;
        error = '';
        try {
            const raw = (await GetHistory('', 500)) as any[];
            runs = (raw ?? []) as SessionRun[];
        } catch (e: any) {
            error = `${$t('history.loadFailedPrefix')}${e?.message ?? String(e)}`;
        } finally {
            loading = false;
        }
    }

    onMount(load);

    function close() {
        dispatch('close');
    }

    function handleKey(e: KeyboardEvent) {
        if (e.key === 'Escape') close();
    }

    function durationMs(r: SessionRun): number {
        if (r.DurationMs && r.DurationMs > 0) return r.DurationMs;
        const start = r.StartedAt ? new Date(r.StartedAt).getTime() : 0;
        const end = r.FinishedAt ? new Date(r.FinishedAt).getTime() : 0;
        if (start > 0 && end > start) return end - start;
        return 0;
    }

    function formatDateTime(s: string | null | undefined): string {
        if (!s) return '';
        const d = new Date(s);
        const t = d.getTime();
        if (!t || isNaN(t)) return '';
        const dd = String(d.getDate()).padStart(2, '0');
        const mm = String(d.getMonth() + 1).padStart(2, '0');
        return `${dd}.${mm} ${formatTime(d)}`;
    }

    function statusColor(status: string): string {
        switch (status) {
            case 'completed':
                return 'text-status-working';
            case 'error':
                return 'text-status-error';
            case 'rate_limited':
                return 'text-status-ratelimit';
            case 'stopped':
                return 'text-text-muted';
            case 'running':
                return 'text-status-starting';
            default:
                return 'text-text-muted';
        }
    }

    function statusLabel(status: string): string {
        if (!status) return '—';
        return status.replace(/_/g, ' ');
    }

    function inDateRange(r: SessionRun): boolean {
        if (!filterFrom && !filterTo) return true;
        const ts = r.StartedAt ? new Date(r.StartedAt).getTime() : 0;
        if (!ts || isNaN(ts)) return false;
        if (filterFrom) {
            const from = new Date(filterFrom).getTime();
            if (!isNaN(from) && ts < from) return false;
        }
        if (filterTo) {
            // inclusive end-of-day for "to"
            const to = new Date(filterTo).getTime() + 24 * 3600 * 1000 - 1;
            if (!isNaN(to) && ts > to) return false;
        }
        return true;
    }

    function compareRuns(a: SessionRun, b: SessionRun): number {
        let av: number | string = 0;
        let bv: number | string = 0;
        switch (sortKey) {
            case 'session':
                av = `${a.Project}/${a.Session}`;
                bv = `${b.Project}/${b.Session}`;
                break;
            case 'started':
                av = a.StartedAt ? new Date(a.StartedAt).getTime() : 0;
                bv = b.StartedAt ? new Date(b.StartedAt).getTime() : 0;
                break;
            case 'duration':
                av = durationMs(a);
                bv = durationMs(b);
                break;
            case 'turns':
                av = a.NumTurns;
                bv = b.NumTurns;
                break;
            case 'tasks':
                av = a.TasksDone;
                bv = b.TasksDone;
                break;
            case 'cost':
                av = a.TotalCostUSD;
                bv = b.TotalCostUSD;
                break;
            case 'status':
                av = a.Status ?? '';
                bv = b.Status ?? '';
                break;
        }
        const cmp = typeof av === 'string' && typeof bv === 'string'
            ? av.localeCompare(bv)
            : ((av as number) - (bv as number));
        return sortDir === 'asc' ? cmp : -cmp;
    }

    function toggleSort(key: SortKey) {
        if (sortKey === key) {
            sortDir = sortDir === 'asc' ? 'desc' : 'asc';
        } else {
            sortKey = key;
            sortDir = key === 'started' ? 'desc' : 'asc';
        }
    }

    function sortIndicator(key: SortKey): string {
        if (sortKey !== key) return '';
        return sortDir === 'asc' ? ' ▲' : ' ▼';
    }

    async function toggleExpand(run: SessionRun) {
        if (expandedRunID === run.ID) {
            expandedRunID = null;
            return;
        }
        expandedRunID = run.ID;
        if (logsByRunID[run.ID] !== undefined) return; // already loaded
        logsLoadingByRunID = { ...logsLoadingByRunID, [run.ID]: true };
        logsErrorByRunID = { ...logsErrorByRunID, [run.ID]: '' };
        try {
            const id = `${run.Project}/${run.Session}`;
            const raw = (await GetSessionLog(id, 0, 1000)) as any[];
            logsByRunID = { ...logsByRunID, [run.ID]: (raw ?? []) as LogEntry[] };
        } catch (e: any) {
            logsErrorByRunID = {
                ...logsErrorByRunID,
                [run.ID]: e?.message ?? String(e),
            };
            logsByRunID = { ...logsByRunID, [run.ID]: [] };
        } finally {
            logsLoadingByRunID = { ...logsLoadingByRunID, [run.ID]: false };
        }
    }

    function clearFilters() {
        filterProject = '';
        filterSession = '';
        filterStatus = '';
        filterFrom = '';
        filterTo = '';
    }

    function logToFormatterEntry(l: LogEntry) {
        return {
            time: l.Timestamp,
            level: l.Level,
            message: l.Message,
            tool_name: l.ToolName,
            tool_input: l.ToolInput,
        };
    }

    $: filtered = (runs ?? [])
        .filter((r) => !filterProject || r.Project === filterProject)
        .filter((r) =>
            !filterSession ||
            r.Session.toLowerCase().includes(filterSession.toLowerCase()),
        )
        .filter((r) => !filterStatus || r.Status === filterStatus)
        .filter(inDateRange)
        .slice()
        .sort(compareRuns);

    // Status values present in the current dataset (for the dropdown).
    $: statusValues = Array.from(new Set((runs ?? []).map((r) => r.Status).filter(Boolean))).sort();
</script>

<svelte:window on:keydown={handleKey} />

<div
    class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
    on:click={close}
    on:keydown={(e) => e.key === 'Escape' && close()}
    role="dialog"
    aria-modal="true"
    tabindex="-1">
    <div
        class="bg-bg-panel border border-bg-border rounded-md shadow-xl w-[1100px] max-w-[96vw] h-[720px] max-h-[94vh] flex flex-col"
        role="document"
        on:click|stopPropagation
        on:keydown|stopPropagation>
        <!-- Header -->
        <div class="px-4 py-3 border-b border-bg-border flex items-center justify-between shrink-0">
            <div class="flex items-center gap-3">
                <h2 class="text-text font-semibold text-base">{$t('history.title')}</h2>
                <span class="text-text-muted text-xs">
                    {runs.length === 1
                        ? $t('history.runCountSingular', { filtered: filtered.length, total: runs.length })
                        : $t('history.runCountPlural', { filtered: filtered.length, total: runs.length })}
                </span>
            </div>
            <div class="flex items-center gap-2">
                <button
                    type="button"
                    on:click={load}
                    class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg">
                    {$t('history.refresh')}
                </button>
                <button
                    class="text-text-muted hover:text-text text-sm px-2 py-0.5"
                    on:click={close}
                    type="button">✕</button>
            </div>
        </div>

        <!-- Filters -->
        <div class="px-4 py-3 border-b border-bg-border shrink-0 grid grid-cols-[1.2fr_1fr_1fr_1fr_1fr_auto] gap-2 items-end">
            <label class="flex flex-col text-xs text-text-muted gap-1">
                {$t('history.filterProject')}
                <select
                    bind:value={filterProject}
                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                    <option value="">{$t('history.allProjects')}</option>
                    {#each $projects as p (p.name)}
                        <option value={p.name}>{p.name}</option>
                    {/each}
                </select>
            </label>
            <label class="flex flex-col text-xs text-text-muted gap-1">
                {$t('history.filterSession')}
                <input
                    type="text"
                    placeholder={$t('history.sessionFilterPlaceholder')}
                    bind:value={filterSession}
                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
            </label>
            <label class="flex flex-col text-xs text-text-muted gap-1">
                {$t('history.filterStatus')}
                <select
                    bind:value={filterStatus}
                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                    <option value="">{$t('history.anyStatus')}</option>
                    {#each statusValues as s (s)}
                        <option value={s}>{statusLabel(s)}</option>
                    {/each}
                </select>
            </label>
            <label class="flex flex-col text-xs text-text-muted gap-1">
                {$t('history.filterFrom')}
                <input
                    type="date"
                    bind:value={filterFrom}
                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
            </label>
            <label class="flex flex-col text-xs text-text-muted gap-1">
                {$t('history.filterTo')}
                <input
                    type="date"
                    bind:value={filterTo}
                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text" />
            </label>
            <button
                type="button"
                on:click={clearFilters}
                class="h-[28px] px-2 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg">
                {$t('history.clear')}
            </button>
        </div>

        <!-- Body -->
        <div class="flex-1 min-h-0 overflow-y-auto">
            {#if loading}
                <div class="text-text-muted text-sm italic py-10 text-center">
                    {$t('history.loadingHistory')}
                </div>
            {:else if error}
                <div class="text-status-error text-sm py-10 text-center">{error}</div>
            {:else if filtered.length === 0}
                <div class="text-text-muted text-sm italic py-10 text-center">
                    {$t('history.noRunsMatch')}
                </div>
            {:else}
                <table class="w-full text-sm border-collapse">
                    <thead class="bg-bg-elevated sticky top-0 z-10 text-text-muted text-xs">
                        <tr>
                            <th class="w-6"></th>
                            <th
                                class="text-left px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('session')}>
                                {$t('history.colSession')}{sortIndicator('session')}
                            </th>
                            <th
                                class="text-left px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('started')}>
                                {$t('history.colStarted')}{sortIndicator('started')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('duration')}>
                                {$t('history.colDuration')}{sortIndicator('duration')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('turns')}>
                                {$t('history.colTurns')}{sortIndicator('turns')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('tasks')}>
                                {$t('history.colTasks')}{sortIndicator('tasks')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('cost')}>
                                {$t('history.colCost')}{sortIndicator('cost')}
                            </th>
                            <th
                                class="text-left px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('status')}>
                                {$t('history.colStatus')}{sortIndicator('status')}
                            </th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each filtered as r (r.ID)}
                            {@const expanded = expandedRunID === r.ID}
                            <tr
                                class="border-t border-bg-border cursor-pointer
                                       {expanded ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/60'}"
                                on:click={() => toggleExpand(r)}>
                                <td class="px-2 py-1.5 text-text-muted text-xs text-center select-none">
                                    {expanded ? '▼' : '▶'}
                                </td>
                                <td class="px-3 py-1.5 text-text">
                                    <span class="font-medium">{r.Project}</span>
                                    <span class="text-text-muted">/{r.Session}</span>
                                    {#if r.Model}
                                        <span class="text-text-dim text-xs ml-2">{r.Model}</span>
                                    {/if}
                                </td>
                                <td class="px-3 py-1.5 text-text-muted font-mono text-xs">
                                    {formatDateTime(r.StartedAt)}
                                </td>
                                <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                    {formatDuration(durationMs(r))}
                                </td>
                                <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                    {r.NumTurns ?? 0}
                                </td>
                                <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                    {r.TasksDone ?? 0}
                                </td>
                                <td class="px-3 py-1.5 text-right text-text font-mono text-xs">
                                    {formatCost(r.TotalCostUSD)}
                                </td>
                                <td class="px-3 py-1.5 text-xs {statusColor(r.Status)}">
                                    {statusLabel(r.Status)}
                                </td>
                            </tr>
                            {#if expanded}
                                <tr class="bg-bg">
                                    <td colspan="8" class="px-0 py-0">
                                        <div class="px-4 py-3 border-t border-b border-bg-border">
                                            <div class="grid grid-cols-4 gap-3 mb-3 text-xs">
                                                <div>
                                                    <div class="text-text-dim">{$t('history.tokensIn')}</div>
                                                    <div class="text-text font-mono">{formatTokens(r.InputTokens)}</div>
                                                </div>
                                                <div>
                                                    <div class="text-text-dim">{$t('history.tokensOut')}</div>
                                                    <div class="text-text font-mono">{formatTokens(r.OutputTokens)}</div>
                                                </div>
                                                <div>
                                                    <div class="text-text-dim">{$t('history.cacheRead')}</div>
                                                    <div class="text-text font-mono">{formatTokens(r.CacheReadTokens)}</div>
                                                </div>
                                                <div>
                                                    <div class="text-text-dim">{$t('history.cacheCreation')}</div>
                                                    <div class="text-text font-mono">{formatTokens(r.CacheCreationTokens)}</div>
                                                </div>
                                            </div>
                                            {#if r.ErrorMsg}
                                                <div class="text-status-error text-xs mb-2 font-mono break-words">
                                                    {r.ErrorMsg}
                                                </div>
                                            {/if}

                                            <div class="text-text-muted text-xs mb-1">{$t('history.logEntries')}</div>
                                            {#if logsLoadingByRunID[r.ID]}
                                                <div class="text-text-muted italic text-xs py-2">{$t('history.loadingLogs')}</div>
                                            {:else if logsErrorByRunID[r.ID]}
                                                <div class="text-status-error text-xs py-2 font-mono break-words">
                                                    {logsErrorByRunID[r.ID]}
                                                </div>
                                            {:else if (logsByRunID[r.ID] ?? []).length === 0}
                                                <div class="text-text-dim italic text-xs py-2">{$t('history.noLogEntries')}</div>
                                            {:else}
                                                <div class="max-h-[280px] overflow-y-auto bg-bg border border-bg-border rounded font-mono text-[12px] leading-5 px-2 py-1">
                                                    {#each logsByRunID[r.ID] ?? [] as l (l.ID)}
                                                        {@const entry = logToFormatterEntry(l)}
                                                        <div class="flex items-start gap-2 py-px {logEntryColor(entry)}">
                                                            <span class="text-text-dim shrink-0 select-none">
                                                                [{formatTime(l.Timestamp)}]
                                                            </span>
                                                            <span class="shrink-0 select-none w-4 text-center">
                                                                {logEntryIcon(entry)}
                                                            </span>
                                                            <span class="whitespace-pre-wrap break-words">
                                                                {l.Message ?? ''}
                                                            </span>
                                                        </div>
                                                    {/each}
                                                </div>
                                            {/if}
                                        </div>
                                    </td>
                                </tr>
                            {/if}
                        {/each}
                    </tbody>
                </table>
            {/if}
        </div>

        <!-- Footer -->
        <div class="px-4 py-2 border-t border-bg-border text-xs text-text-muted shrink-0">
            {$t('history.footerHint')}
        </div>
    </div>
</div>
