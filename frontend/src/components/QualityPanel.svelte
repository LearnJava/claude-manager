<script lang="ts">
    // "Quality" tab of the Experience panel: the GUI side of reference cases
    // (WASTE-TASKS.md WS-06), quality labels (WS-11) and the configuration
    // benchmark (WS-12), which until now were reachable only through MCP.
    // Every block says how to switch its feature on while it is off.
    import { t } from '../lib/i18n';
    import { formatTokens, formatDuration } from '../lib/formatters';
    import { MODELS } from '../lib/models';
    import {
        GetConfig,
        GetReferenceReport,
        ReplayReferenceCase,
        GetTaskLabels,
        GetBenchmarkReport,
        RunBenchmark,
    } from '../../wailsjs/go/main/App';

    export let project = '';

    let flags = { ReferenceCases: false, QualityLabels: false, Benchmark: false };
    let refs: any = { Cases: [] };
    let labels: any = { Total: 0, Tokens: 0, Counts: [], Recent: [] };
    let bench: any = null;
    let error = '';
    let replaying: Record<number, boolean> = {};
    let replayModel: Record<number, string> = {};
    let benchModels: string[] = ['sonnet', 'opus'];
    let benchRepeats = 2;
    let benchRounds = 1;
    let benchBusy = false;
    let lastProject: string | null = null;

    $: if (project !== lastProject) {
        lastProject = project;
        void load();
    }

    export async function load() {
        error = '';
        try {
            const cfg: any = await GetConfig();
            flags = {
                ReferenceCases: !!cfg?.Optimization?.ReferenceCases,
                QualityLabels: !!cfg?.Optimization?.QualityLabels,
                Benchmark: !!cfg?.Optimization?.Benchmark,
            };
            if (!project) return;
            const [r, l, b] = await Promise.all([
                GetReferenceReport(project).catch(() => null),
                GetTaskLabels(project).catch(() => null),
                GetBenchmarkReport(project, 0).catch(() => null),
            ]);
            refs = { Cases: (r as any)?.Cases ?? [] };
            labels = {
                Total: (l as any)?.Total ?? 0,
                Tokens: (l as any)?.Tokens ?? 0,
                Counts: (l as any)?.Counts ?? [],
                Recent: (l as any)?.Recent ?? [],
            };
            bench = b && (b as any).RunID ? b : null;
        } catch (e: any) {
            error = e?.message ?? String(e);
        }
    }

    async function replay(c: any) {
        const model = replayModel[c.CaseID] || 'sonnet';
        replaying = { ...replaying, [c.CaseID]: true };
        error = '';
        try {
            await ReplayReferenceCase(project, c.CaseID, model, '');
            await load();
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            replaying = { ...replaying, [c.CaseID]: false };
        }
    }

    function toggleBenchModel(m: string) {
        benchModels = benchModels.includes(m) ? benchModels.filter((x) => x !== m) : [...benchModels, m];
    }

    async function runBench() {
        benchBusy = true;
        error = '';
        try {
            const spec: any = {
                name: `${benchModels.join(' vs ')} ×${benchRepeats}`,
                case_ids: [],
                tasks: [],
                configs: benchModels.map((m) => ({ name: m, model: m })),
                repeats: benchRepeats,
                rounds: benchRounds,
            };
            bench = await RunBenchmark(project, spec);
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            benchBusy = false;
        }
    }

    const sec = (s: number) => formatDuration((s ?? 0) * 1000);
    const mean = (st: any, f: (n: number) => string) =>
        st && st.N > 0 ? `${f(st.Mean)}${st.N > 1 ? ` ±${f(st.StdDev)}` : ''}` : '—';
    const verdictClass = (v: string) =>
        v === 'differs' ? 'text-status-error' : v === 'within_noise' ? 'text-text' : 'text-text-muted';
</script>

<div class="text-xs" data-testid="quality-panel">
    {#if !project}
        <div class="px-4 py-3 text-text-muted italic">{$t('quality.pickProject')}</div>
    {/if}
    {#if error}
        <div class="px-4 py-2 text-status-error" data-testid="quality-error">{error}</div>
    {/if}

    <!-- Reference cases (WS-06) -->
    <div class="px-4 py-3 border-b border-bg-border" data-testid="quality-refs">
        <h3 class="text-text text-sm font-medium mb-1">{$t('quality.refs.title')}</h3>
        <p class="text-text-muted mb-2">{$t('quality.refs.intro')}</p>
        {#if !flags.ReferenceCases}
            <div class="text-text-muted italic" data-testid="quality-refs-off">{$t('quality.refs.off')}</div>
        {:else if project && refs.Cases.length === 0}
            <div class="text-text-muted italic">{$t('quality.refs.empty')}</div>
        {/if}
        {#each project ? refs.Cases : [] as c (c.CaseID)}
            <div class="border border-bg-border rounded p-2 mb-2" data-testid="quality-ref-case">
                <div class="flex items-center gap-2">
                    <span class="text-text font-mono">#{c.CaseID} {c.TaskPtr}</span>
                    <span class="text-text-muted font-mono">{(c.BaseCommit ?? '').slice(0, 8)}</span>
                    {#if c.InvalidReason}
                        <span class="text-status-error">{$t('quality.refs.invalid', { reason: c.InvalidReason })}</span>
                    {/if}
                </div>
                <table class="w-full mt-1">
                    <thead class="text-text-muted text-left">
                        <tr>
                            <th class="font-normal">{$t('quality.col.run')}</th>
                            <th class="font-normal">{$t('quality.col.model')}</th>
                            <th class="font-normal">{$t('quality.col.result')}</th>
                            <th class="font-normal">{$t('quality.col.rounds')}</th>
                            <th class="font-normal">{$t('quality.col.tokens')}</th>
                            <th class="font-normal">{$t('quality.col.time')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each c.Runs ?? [] as r}
                            <tr class="text-text">
                                <td>{r.Original ? $t('quality.refs.original') : $t('quality.refs.replay')}</td>
                                <td class="font-mono">{r.Model || '—'}{r.Effort ? ` / ${r.Effort}` : ''}</td>
                                <td class={r.Passed ? 'text-status-working' : 'text-status-error'}>
                                    {r.Passed ? $t('quality.passed') : $t('quality.failed')}
                                </td>
                                <td>{r.Rounds}</td>
                                <td>{formatTokens(r.Tokens)}</td>
                                <td>{sec(r.DurationSec)}</td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
                {#if !c.InvalidReason}
                    <div class="flex items-center gap-2 mt-2">
                        <select
                            bind:value={replayModel[c.CaseID]}
                            class="bg-bg border border-bg-border rounded px-1 py-0.5 text-text">
                            {#each MODELS as m}
                                <option value={m.value}>{m.label}</option>
                            {/each}
                        </select>
                        <button
                            type="button"
                            data-testid="quality-replay"
                            disabled={replaying[c.CaseID]}
                            on:click={() => replay(c)}
                            class="px-2 py-1 rounded border border-blue-500 hover:bg-bg-elevated/60 disabled:opacity-50">
                            {replaying[c.CaseID] ? $t('quality.refs.replaying') : $t('quality.refs.replayBtn')}
                        </button>
                    </div>
                {/if}
            </div>
        {/each}
    </div>

    <!-- Quality labels (WS-11) -->
    <div class="px-4 py-3 border-b border-bg-border" data-testid="quality-labels">
        <h3 class="text-text text-sm font-medium mb-1">{$t('quality.labels.title')}</h3>
        <p class="text-text-muted mb-2">{$t('quality.labels.intro')}</p>
        {#if !flags.QualityLabels}
            <div class="text-text-muted italic mb-1" data-testid="quality-labels-off">{$t('quality.labels.off')}</div>
        {/if}
        {#if project && labels.Total === 0}
            <div class="text-text-muted italic">{$t('quality.labels.empty')}</div>
        {:else if project}
            <div class="flex flex-wrap gap-2 mb-2">
                {#each labels.Counts as lc}
                    <span class="px-2 py-0.5 rounded bg-bg-elevated border border-bg-border text-text">
                        {$t(`quality.label.${lc.Label}`)}: {lc.Count}
                    </span>
                {/each}
                <span class="text-text-muted">{$t('quality.labels.cost', { tokens: formatTokens(labels.Tokens) })}</span>
            </div>
            {#each labels.Recent as l}
                <div class="border-t border-bg-border py-1" data-testid="quality-label-row">
                    <span class="text-text font-mono">{l.TaskPtr}</span>
                    <span class="ml-2 text-status-error">{$t(`quality.label.${l.Label}`)}</span>
                    <div class="text-text-muted">{l.Reason}</div>
                </div>
            {/each}
        {/if}
    </div>

    <!-- Benchmark (WS-12) -->
    <div class="px-4 py-3" data-testid="quality-bench">
        <h3 class="text-text text-sm font-medium mb-1">{$t('quality.bench.title')}</h3>
        <p class="text-text-muted mb-2">{$t('quality.bench.intro')}</p>
        {#if !flags.Benchmark}
            <div class="text-text-muted italic mb-2" data-testid="quality-bench-off">{$t('quality.bench.off')}</div>
        {:else if project}
            <div class="flex flex-wrap items-center gap-3 mb-2" data-testid="quality-bench-form">
                <span class="text-text-muted">{$t('quality.bench.models')}</span>
                {#each MODELS as m}
                    <label class="flex items-center gap-1 text-text">
                        <input
                            type="checkbox"
                            checked={benchModels.includes(m.value)}
                            on:change={() => toggleBenchModel(m.value)} />
                        {m.label}
                    </label>
                {/each}
                <label class="flex items-center gap-1 text-text-muted">
                    {$t('quality.bench.repeats')}
                    <input type="number" min="1" bind:value={benchRepeats}
                        class="w-14 bg-bg border border-bg-border rounded px-1 py-0.5 text-text" />
                </label>
                <label class="flex items-center gap-1 text-text-muted">
                    {$t('quality.bench.rounds')}
                    <input type="number" min="0" bind:value={benchRounds}
                        class="w-14 bg-bg border border-bg-border rounded px-1 py-0.5 text-text" />
                </label>
                <button
                    type="button"
                    data-testid="quality-bench-run"
                    disabled={benchBusy || benchModels.length === 0 || refs.Cases.length === 0}
                    on:click={runBench}
                    class="px-2 py-1 rounded border border-blue-500 hover:bg-bg-elevated/60 disabled:opacity-50">
                    {$t('quality.bench.run')}
                </button>
            </div>
            <div class="text-text-muted mb-2">{$t('quality.bench.costNote')}</div>
        {/if}
        {#if bench}
            <div class="border border-bg-border rounded p-2" data-testid="quality-bench-report">
                <div class="text-text mb-1">
                    <span class="font-medium">{bench.Name || `#${bench.RunID}`}</span>
                    <span class="ml-2 text-text-muted">{$t('quality.bench.status', {
                        status: bench.Status, stored: bench.StoredTrials, planned: bench.PlannedTrials })}</span>
                </div>
                <table class="w-full">
                    <thead class="text-text-muted text-left">
                        <tr>
                            <th class="font-normal">{$t('quality.col.config')}</th>
                            <th class="font-normal">{$t('quality.col.passRate')}</th>
                            <th class="font-normal">{$t('quality.col.rounds')}</th>
                            <th class="font-normal">{$t('quality.col.tokens')}</th>
                            <th class="font-normal">{$t('quality.col.time')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each bench.Configs ?? [] as c}
                            <tr class="text-text">
                                <td class="font-mono">{c.Config.Name}</td>
                                <td>{c.Trials ? `${Math.round(c.PassRate * 100)}% (${c.Passed}/${c.Trials})` : '—'}</td>
                                <td>{mean(c.Rounds, (n) => n.toFixed(1))}</td>
                                <td>{mean(c.Tokens, (n) => formatTokens(Math.round(n)))}</td>
                                <td>{mean(c.DurationSec, (n) => sec(Math.round(n)))}</td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
                {#if (bench.Comparisons ?? []).length > 0}
                    <div class="mt-2 text-text-muted">{$t('quality.bench.vs', { baseline: bench.Baseline })}</div>
                    {#each bench.Comparisons as cmp}
                        <div class="font-mono">
                            {cmp.Config} · {cmp.Metric}:
                            <span class={verdictClass(cmp.Verdict)}>{$t(`quality.verdict.${cmp.Verdict}`)}</span>
                            {#if cmp.Direction}<span class="text-text-muted"> ({cmp.Direction})</span>{/if}
                        </div>
                    {/each}
                {/if}
                {#each bench.Warnings ?? [] as w}
                    <div class="text-status-ratelimit mt-1">⚠ {w}</div>
                {/each}
            </div>
        {:else if project && flags.Benchmark}
            <div class="text-text-muted italic">{$t('quality.bench.empty')}</div>
        {/if}
    </div>
</div>
