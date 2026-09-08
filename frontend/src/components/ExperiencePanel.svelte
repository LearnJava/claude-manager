<script lang="ts">
    import { createEventDispatcher, onMount } from 'svelte';
    import { projects } from '../stores/projects';
    import { formatPercent, formatTime, formatTokens } from '../lib/formatters';
    import {
        fetchActionSamples,
        fetchTopActions,
        type ActionRow,
        type SignatureStat,
    } from '../stores/experience';

    const dispatch = createEventDispatcher();

    // This modal gains more tabs as later LN items land (LN-04 Permissions,
    // LN-06 Journal, ...) — one modal, not a new one per tab (see LEARN-TASKS.md
    // LN-03 "UI").
    type Tab = 'actions';
    let tab: Tab = 'actions';

    type SortKey = 'sig' | 'count' | 'runs' | 'errors' | 'tokens' | 'last';

    let project = '';
    let days = 30;
    const dayOptions: { id: number; label: string }[] = [
        { id: 1, label: 'Today' },
        { id: 7, label: '7 days' },
        { id: 30, label: '30 days' },
        { id: 90, label: '90 days' },
    ];

    let stats: SignatureStat[] = [];
    let loading = true;
    let error = '';

    let sortKey: SortKey = 'count';
    let sortDir: 'asc' | 'desc' = 'desc';

    // Row expansion: clicking a signature loads its sample rows.
    let expandedSig: string | null = null;
    let samplesBySig: Record<string, ActionRow[] | undefined> = {};
    let samplesLoadingBySig: Record<string, boolean> = {};
    let samplesErrorBySig: Record<string, string> = {};

    function close() {
        dispatch('close');
    }

    function handleKey(e: KeyboardEvent) {
        if (e.key === 'Escape') close();
    }

    async function load() {
        if (!project) {
            stats = [];
            loading = false;
            return;
        }
        loading = true;
        error = '';
        try {
            stats = await fetchTopActions(project, days);
        } catch (e: any) {
            error = `Failed to load actions: ${e?.message ?? String(e)}`;
            stats = [];
        } finally {
            loading = false;
        }
    }

    onMount(() => {
        if (!project && $projects.length > 0) {
            project = $projects[0].name;
        } else {
            load();
        }
    });

    // Reload on project/period change (also fires once project is picked
    // above, since it starts empty).
    $: if (project || days) {
        load();
    }

    function compareStats(a: SignatureStat, b: SignatureStat): number {
        let av: number | string = 0;
        let bv: number | string = 0;
        switch (sortKey) {
            case 'sig':
                av = a.Sig ?? '';
                bv = b.Sig ?? '';
                break;
            case 'count':
                av = a.Count;
                bv = b.Count;
                break;
            case 'runs':
                av = a.DistinctRuns;
                bv = b.DistinctRuns;
                break;
            case 'errors':
                av = a.ErrorRate;
                bv = b.ErrorRate;
                break;
            case 'tokens':
                av = a.SumOutTokens;
                bv = b.SumOutTokens;
                break;
            case 'last':
                av = a.LastSeen ? new Date(a.LastSeen).getTime() : 0;
                bv = b.LastSeen ? new Date(b.LastSeen).getTime() : 0;
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
            sortDir = key === 'sig' ? 'asc' : 'desc';
        }
    }

    function sortIndicator(key: SortKey): string {
        if (sortKey !== key) return '';
        return sortDir === 'asc' ? ' ▲' : ' ▼';
    }

    async function toggleExpand(row: SignatureStat) {
        if (expandedSig === row.Sig) {
            expandedSig = null;
            return;
        }
        expandedSig = row.Sig;
        if (samplesBySig[row.Sig] !== undefined) return; // already loaded
        samplesLoadingBySig = { ...samplesLoadingBySig, [row.Sig]: true };
        samplesErrorBySig = { ...samplesErrorBySig, [row.Sig]: '' };
        try {
            const rows = await fetchActionSamples(project, row.Sig, 20);
            samplesBySig = { ...samplesBySig, [row.Sig]: rows };
        } catch (e: any) {
            samplesErrorBySig = {
                ...samplesErrorBySig,
                [row.Sig]: e?.message ?? String(e),
            };
            samplesBySig = { ...samplesBySig, [row.Sig]: [] };
        } finally {
            samplesLoadingBySig = { ...samplesLoadingBySig, [row.Sig]: false };
        }
    }

    $: sorted = (stats ?? []).slice().sort(compareStats);
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
        class="bg-bg-panel border border-bg-border rounded-md shadow-xl w-[1000px] max-w-[96vw] h-[680px] max-h-[94vh] flex flex-col"
        role="document"
        on:click|stopPropagation
        on:keydown|stopPropagation>
        <!-- Header -->
        <div class="px-4 py-3 border-b border-bg-border flex items-center justify-between shrink-0">
            <div class="flex items-center gap-3">
                <h2 class="text-text font-semibold text-base">Experience</h2>
                <div class="flex items-center gap-1 text-xs">
                    <button
                        type="button"
                        class="px-2 py-1 rounded border
                            {tab === 'actions'
                                ? 'bg-bg-elevated border-blue-500 text-text'
                                : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                        Actions
                    </button>
                </div>
            </div>
            <div class="flex items-center gap-2">
                <button
                    type="button"
                    on:click={load}
                    class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg">
                    Refresh
                </button>
                <button
                    class="text-text-muted hover:text-text text-sm px-2 py-0.5"
                    on:click={close}
                    type="button">✕</button>
            </div>
        </div>

        <!-- Filters -->
        <div class="px-4 py-3 border-b border-bg-border shrink-0 flex items-end gap-2">
            <label class="flex flex-col text-xs text-text-muted gap-1">
                Project
                <select
                    bind:value={project}
                    class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                    {#each $projects as p (p.name)}
                        <option value={p.name}>{p.name}</option>
                    {/each}
                </select>
            </label>
            <div class="flex items-center gap-1 text-xs">
                {#each dayOptions as opt (opt.id)}
                    <button
                        type="button"
                        on:click={() => (days = opt.id)}
                        class="px-2 py-1 rounded border
                            {days === opt.id
                                ? 'bg-bg-elevated border-blue-500 text-text'
                                : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                        {opt.label}
                    </button>
                {/each}
            </div>
        </div>

        <!-- Body -->
        <div class="flex-1 min-h-0 overflow-y-auto">
            {#if !project}
                <div class="text-text-muted text-sm italic py-10 text-center">
                    No project configured.
                </div>
            {:else if loading}
                <div class="text-text-muted text-sm italic py-10 text-center">
                    Loading actions…
                </div>
            {:else if error}
                <div class="text-status-error text-sm py-10 text-center">{error}</div>
            {:else if sorted.length === 0}
                <div class="text-text-muted text-sm italic py-10 text-center">
                    No recorded actions for this project/period. Enable
                    <code class="font-mono">[optimization] experience_tracking</code> to start
                    collecting them.
                </div>
            {:else}
                <table class="w-full text-sm border-collapse">
                    <thead class="bg-bg-elevated sticky top-0 z-10 text-text-muted text-xs">
                        <tr>
                            <th class="w-6"></th>
                            <th
                                class="text-left px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('sig')}>
                                Signature{sortIndicator('sig')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('count')}>
                                N{sortIndicator('count')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('runs')}>
                                Runs{sortIndicator('runs')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('errors')}>
                                Errors{sortIndicator('errors')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('tokens')}>
                                Out tokens{sortIndicator('tokens')}
                            </th>
                            <th
                                class="text-left px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('last')}>
                                Last seen{sortIndicator('last')}
                            </th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each sorted as row (row.Sig)}
                            {@const expanded = expandedSig === row.Sig}
                            <tr
                                class="border-t border-bg-border cursor-pointer
                                       {expanded ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/60'}"
                                on:click={() => toggleExpand(row)}>
                                <td class="px-2 py-1.5 text-text-muted text-xs text-center select-none">
                                    {expanded ? '▼' : '▶'}
                                </td>
                                <td class="px-3 py-1.5 text-text font-mono text-xs break-all">
                                    {row.Sig}
                                </td>
                                <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                    {row.Count}
                                </td>
                                <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                    {row.DistinctRuns}
                                </td>
                                <td
                                    class="px-3 py-1.5 text-right font-mono text-xs
                                        {row.ErrorRate > 0 ? 'text-status-error' : 'text-text-muted'}">
                                    {formatPercent(row.ErrorRate)}
                                </td>
                                <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                    {formatTokens(row.SumOutTokens)}
                                </td>
                                <td class="px-3 py-1.5 text-text-muted font-mono text-xs">
                                    {row.LastSeen ? formatTime(row.LastSeen) : '—'}
                                </td>
                            </tr>
                            {#if expanded}
                                <tr class="bg-bg">
                                    <td colspan="7" class="px-0 py-0">
                                        <div class="px-4 py-3 border-t border-b border-bg-border">
                                            {#if (row.SampleArgs ?? []).length > 0}
                                                <div class="text-text-dim text-xs mb-2">
                                                    Sample args:
                                                    {#each row.SampleArgs ?? [] as a, i (i)}
                                                        <span class="font-mono text-text-muted">{a}</span>{i < (row.SampleArgs?.length ?? 0) - 1 ? ', ' : ''}
                                                    {/each}
                                                </div>
                                            {/if}
                                            <div class="text-text-muted text-xs mb-1">Examples</div>
                                            {#if samplesLoadingBySig[row.Sig]}
                                                <div class="text-text-muted italic text-xs py-2">Loading examples…</div>
                                            {:else if samplesErrorBySig[row.Sig]}
                                                <div class="text-status-error text-xs py-2 font-mono break-words">
                                                    {samplesErrorBySig[row.Sig]}
                                                </div>
                                            {:else if (samplesBySig[row.Sig] ?? []).length === 0}
                                                <div class="text-text-dim italic text-xs py-2">No sample rows.</div>
                                            {:else}
                                                <div class="max-h-[240px] overflow-y-auto bg-bg border border-bg-border rounded font-mono text-[12px] leading-5 px-2 py-1">
                                                    {#each samplesBySig[row.Sig] ?? [] as s (s.ID)}
                                                        <div class="flex items-start gap-2 py-px {s.IsError ? 'text-status-error' : 'text-text'}">
                                                            <span class="text-text-dim shrink-0 select-none">
                                                                [{formatTime(s.Timestamp)}]
                                                            </span>
                                                            <span class="text-text-muted shrink-0">{s.Session}</span>
                                                            <span class="whitespace-pre-wrap break-words">
                                                                {s.Arg || '(no arg)'}
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
            Click a signature to load its recorded examples.
        </div>
    </div>
</div>
