<script lang="ts">
    import { createEventDispatcher, onMount } from 'svelte';
    import {
        GetDailyCost,
        GetHistory,
        GetProjectCost,
        GetRateLimitStatus,
    } from '../../wailsjs/go/main/App';
    import { projects } from '../stores/projects';
    import { formatCost, formatPercent } from '../lib/formatters';

    const dispatch = createEventDispatcher();

    interface SessionRun {
        ID: number;
        Project: string;
        Session: string;
        Model: string;
        StartedAt: string;
        Status: string;
        TasksDone: number;
        TotalCostUSD: number;
        InputTokens: number;
        OutputTokens: number;
        CacheReadTokens: number;
        CacheCreationTokens: number;
        NumTurns: number;
    }

    interface RateLimit {
        status?: string;
        Status?: string;
        utilization?: number;
        Utilization?: number;
        rateLimitType?: string;
        RateLimitType?: string;
        resetsAt?: number;
        ResetsAt?: number;
        [k: string]: any;
    }

    type Period = 'today' | 'week' | 'month';

    let period: Period = 'week';

    const periodOptions: { id: Period; label: string }[] = [
        { id: 'today', label: 'Today' },
        { id: 'week', label: 'This week' },
        { id: 'month', label: 'This month' },
    ];

    let runs: SessionRun[] = [];
    let dailyByDate: { date: string; cost: number }[] = [];
    let projectCosts: { project: string; cost: number }[] = [];
    let rateLimit: RateLimit | null = null;

    let loading = true;
    let error = '';

    function close() {
        dispatch('close');
    }

    function handleKey(e: KeyboardEvent) {
        if (e.key === 'Escape') close();
    }

    function periodDays(p: Period): number {
        if (p === 'today') return 1;
        if (p === 'week') return 7;
        return 30;
    }

    function isoDate(d: Date): string {
        const yy = d.getFullYear();
        const mm = String(d.getMonth() + 1).padStart(2, '0');
        const dd = String(d.getDate()).padStart(2, '0');
        return `${yy}-${mm}-${dd}`;
    }

    function startOfPeriod(p: Period): Date {
        const d = new Date();
        d.setHours(0, 0, 0, 0);
        if (p === 'today') return d;
        const days = periodDays(p) - 1;
        d.setDate(d.getDate() - days);
        return d;
    }

    function inPeriod(r: SessionRun): boolean {
        const start = startOfPeriod(period).getTime();
        const end = Date.now();
        if (!r.StartedAt) return false;
        const t = new Date(r.StartedAt).getTime();
        if (!t || isNaN(t)) return false;
        return t >= start && t <= end;
    }

    async function load() {
        loading = true;
        error = '';
        try {
            // 1. Pull recent runs for by-model breakdown and totals.
            const rawRuns = (await GetHistory('', 2000)) as any[];
            runs = (rawRuns ?? []) as SessionRun[];

            // 2. Daily cost across the period.
            const days = periodDays(period);
            const today = new Date();
            today.setHours(0, 0, 0, 0);
            const daily: { date: string; cost: number }[] = [];
            for (let i = days - 1; i >= 0; i--) {
                const d = new Date(today);
                d.setDate(d.getDate() - i);
                const iso = isoDate(d);
                try {
                    const c = (await GetDailyCost(iso)) as number;
                    daily.push({ date: iso, cost: Number(c) || 0 });
                } catch {
                    daily.push({ date: iso, cost: 0 });
                }
            }
            dailyByDate = daily;

            // 3. Per-project cost across the period.
            const projList = $projects;
            const perProj: { project: string; cost: number }[] = [];
            for (const p of projList) {
                try {
                    const c = (await GetProjectCost(p.name, days)) as number;
                    perProj.push({ project: p.name, cost: Number(c) || 0 });
                } catch {
                    perProj.push({ project: p.name, cost: 0 });
                }
            }
            // Sort descending by cost.
            perProj.sort((a, b) => b.cost - a.cost);
            projectCosts = perProj;

            // 4. Current rate limit status.
            try {
                rateLimit = (await GetRateLimitStatus()) as RateLimit | null;
            } catch {
                rateLimit = null;
            }
        } catch (e: any) {
            error = `Failed to load dashboard: ${e?.message ?? String(e)}`;
        } finally {
            loading = false;
        }
    }

    onMount(load);
    // Reload when the user switches period.
    $: if (period) {
        load();
    }

    // ---- Derived metrics ----

    // Runs within the active period.
    $: periodRuns = (runs ?? []).filter(inPeriod);

    // Total from the daily aggregates (authoritative since daily_metrics is
    // what the manager increments on each completed run).
    $: dailyTotal = dailyByDate.reduce((sum, d) => sum + d.cost, 0);

    // Cost-by-model — derived from session_runs in the period.
    $: byModel = (() => {
        const map = new Map<string, number>();
        for (const r of periodRuns) {
            const m = r.Model || 'unknown';
            map.set(m, (map.get(m) ?? 0) + (r.TotalCostUSD || 0));
        }
        const arr = Array.from(map.entries()).map(([model, cost]) => ({ model, cost }));
        arr.sort((a, b) => b.cost - a.cost);
        return arr;
    })();

    $: maxModelCost = byModel.reduce((m, x) => Math.max(m, x.cost), 0);
    $: maxProjectCost = projectCosts.reduce((m, x) => Math.max(m, x.cost), 0);
    $: maxDailyCost = dailyByDate.reduce((m, d) => Math.max(m, d.cost), 0);

    $: projectTotal = projectCosts.reduce((s, x) => s + x.cost, 0);

    // Cache efficiency — reads / (reads + creation) across periodRuns.
    $: cacheStats = (() => {
        let read = 0;
        let creation = 0;
        for (const r of periodRuns) {
            read += r.CacheReadTokens || 0;
            creation += r.CacheCreationTokens || 0;
        }
        const total = read + creation;
        const ratio = total > 0 ? read / total : 0;
        return { read, creation, ratio };
    })();

    // Avg cost per task — total cost / total tasks (skip runs with 0 tasks).
    $: avgCostPerTask = (() => {
        let totalTasks = 0;
        let totalCost = 0;
        for (const r of periodRuns) {
            const tasks = r.TasksDone || 0;
            if (tasks > 0) {
                totalTasks += tasks;
                totalCost += r.TotalCostUSD || 0;
            }
        }
        return totalTasks > 0 ? totalCost / totalTasks : 0;
    })();

    $: rlUtil = rateLimit
        ? Number(rateLimit.utilization ?? rateLimit.Utilization ?? 0)
        : 0;
    $: rlType = rateLimit
        ? String(rateLimit.rateLimitType ?? rateLimit.RateLimitType ?? '')
        : '';

    function dayLabel(iso: string): string {
        const d = new Date(iso + 'T00:00:00');
        const t = d.getTime();
        if (!t || isNaN(t)) return iso;
        return d.toLocaleDateString(undefined, { weekday: 'short' });
    }

    function modelColor(model: string): string {
        const m = (model || '').toLowerCase();
        if (m.includes('opus')) return 'bg-purple-500';
        if (m.includes('sonnet')) return 'bg-blue-500';
        if (m.includes('haiku')) return 'bg-emerald-500';
        return 'bg-text-muted';
    }
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
        class="bg-bg-panel border border-bg-border rounded-md shadow-xl w-[960px] max-w-[96vw] h-[680px] max-h-[94vh] flex flex-col"
        role="document"
        on:click|stopPropagation
        on:keydown|stopPropagation>
        <!-- Header -->
        <div class="px-4 py-3 border-b border-bg-border flex items-center justify-between shrink-0">
            <h2 class="text-text font-semibold text-base">Cost Dashboard</h2>
            <div class="flex items-center gap-3">
                <div class="flex items-center gap-1 text-xs">
                    {#each periodOptions as opt (opt.id)}
                        <button
                            type="button"
                            on:click={() => (period = opt.id)}
                            class="px-2 py-1 rounded border
                                {period === opt.id
                                    ? 'bg-bg-elevated border-blue-500 text-text'
                                    : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                            {opt.label}
                        </button>
                    {/each}
                </div>
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

        <!-- Body -->
        <div class="flex-1 min-h-0 overflow-y-auto px-5 py-5">
            {#if loading && runs.length === 0}
                <div class="text-text-muted text-sm italic py-10 text-center">
                    Loading dashboard…
                </div>
            {:else if error}
                <div class="text-status-error text-sm py-10 text-center">{error}</div>
            {:else}
                <!-- Total + KPI row -->
                <section class="mb-6 grid grid-cols-4 gap-3">
                    <div class="bg-bg-elevated border border-bg-border rounded p-3">
                        <div class="text-text-muted text-xs">Total cost</div>
                        <div class="text-text text-2xl font-semibold mt-1">{formatCost(dailyTotal)}</div>
                        <div class="text-text-dim text-xs mt-1">
                            {period === 'today' ? 'Today' : period === 'week' ? 'Last 7 days' : 'Last 30 days'}
                        </div>
                    </div>
                    <div class="bg-bg-elevated border border-bg-border rounded p-3">
                        <div class="text-text-muted text-xs">Cache efficiency</div>
                        <div class="text-text text-2xl font-semibold mt-1">
                            {formatPercent(cacheStats.ratio)}
                        </div>
                        <div class="text-text-dim text-xs mt-1">
                            reads / (reads + creation)
                        </div>
                    </div>
                    <div class="bg-bg-elevated border border-bg-border rounded p-3">
                        <div class="text-text-muted text-xs">Avg cost per task</div>
                        <div class="text-text text-2xl font-semibold mt-1">
                            {formatCost(avgCostPerTask)}
                        </div>
                        <div class="text-text-dim text-xs mt-1">
                            {periodRuns.length} run{periodRuns.length === 1 ? '' : 's'} in period
                        </div>
                    </div>
                    <div class="bg-bg-elevated border border-bg-border rounded p-3">
                        <div class="text-text-muted text-xs">Rate limit</div>
                        <div class="text-text text-2xl font-semibold mt-1
                            {rlUtil >= 0.85 ? 'text-status-error' : rlUtil >= 0.6 ? 'text-status-ratelimit' : ''}">
                            {rateLimit ? formatPercent(rlUtil) : '—'}
                        </div>
                        <div class="text-text-dim text-xs mt-1">
                            {rlType ? `${rlType.replace(/_/g, '-')} window` : 'no active window'}
                        </div>
                    </div>
                </section>

                <!-- Cost by model -->
                <section class="mb-6">
                    <h3 class="text-text font-semibold text-sm mb-2">Cost by model</h3>
                    {#if byModel.length === 0}
                        <div class="text-text-dim text-xs italic">No runs in selected period.</div>
                    {:else}
                        <div class="space-y-2">
                            {#each byModel as row (row.model)}
                                {@const pct = maxModelCost > 0 ? (row.cost / maxModelCost) * 100 : 0}
                                <div>
                                    <div class="flex justify-between text-xs mb-0.5">
                                        <span class="text-text">{row.model}</span>
                                        <span class="text-text-muted font-mono">{formatCost(row.cost)}</span>
                                    </div>
                                    <div class="h-3 bg-bg-elevated rounded overflow-hidden">
                                        <div
                                            class="h-full {modelColor(row.model)}"
                                            style="width: {pct.toFixed(1)}%"></div>
                                    </div>
                                </div>
                            {/each}
                        </div>
                    {/if}
                </section>

                <!-- Cost by project -->
                <section class="mb-6">
                    <h3 class="text-text font-semibold text-sm mb-2">Cost by project</h3>
                    {#if projectCosts.length === 0}
                        <div class="text-text-dim text-xs italic">No projects configured.</div>
                    {:else}
                        <div class="space-y-2">
                            {#each projectCosts as row (row.project)}
                                {@const pct = maxProjectCost > 0 ? (row.cost / maxProjectCost) * 100 : 0}
                                {@const share = projectTotal > 0 ? (row.cost / projectTotal) * 100 : 0}
                                <div>
                                    <div class="flex justify-between text-xs mb-0.5">
                                        <span class="text-text">{row.project}</span>
                                        <span class="text-text-muted font-mono">
                                            {formatCost(row.cost)}
                                            <span class="text-text-dim ml-2">({share.toFixed(0)}%)</span>
                                        </span>
                                    </div>
                                    <div class="h-3 bg-bg-elevated rounded overflow-hidden">
                                        <div
                                            class="h-full bg-blue-500"
                                            style="width: {pct.toFixed(1)}%"></div>
                                    </div>
                                </div>
                            {/each}
                        </div>
                    {/if}
                </section>

                <!-- Daily breakdown -->
                <section class="mb-2">
                    <h3 class="text-text font-semibold text-sm mb-2">Daily breakdown</h3>
                    {#if dailyByDate.length === 0}
                        <div class="text-text-dim text-xs italic">No data.</div>
                    {:else}
                        <div class="flex items-end gap-2 h-32 bg-bg-elevated border border-bg-border rounded p-3">
                            {#each dailyByDate as d (d.date)}
                                {@const heightPct = maxDailyCost > 0 ? (d.cost / maxDailyCost) * 100 : 0}
                                <div class="flex-1 flex flex-col items-center justify-end gap-1 min-w-[14px]">
                                    <span class="text-text-dim text-[10px] font-mono leading-none">
                                        {d.cost > 0 ? formatCost(d.cost) : ''}
                                    </span>
                                    <div
                                        class="w-full bg-blue-500 rounded-sm"
                                        style="height: {heightPct.toFixed(1)}%; min-height: {d.cost > 0 ? '2px' : '0'}"
                                        title="{d.date}: {formatCost(d.cost)}"></div>
                                    <span class="text-text-muted text-[10px] leading-none">
                                        {dayLabel(d.date)}
                                    </span>
                                </div>
                            {/each}
                        </div>
                    {/if}
                </section>
            {/if}
        </div>

        <!-- Footer -->
        <div class="px-4 py-2 border-t border-bg-border text-xs text-text-muted shrink-0 flex justify-between">
            <span>Aggregated from completed session runs.</span>
            {#if loading && runs.length > 0}
                <span class="italic">Refreshing…</span>
            {/if}
        </div>
    </div>
</div>
