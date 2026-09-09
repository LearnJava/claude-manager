<script lang="ts">
    import { createEventDispatcher, onMount } from 'svelte';
    import { projects } from '../stores/projects';
    import { formatDuration, formatPercent, formatTime, formatTokens } from '../lib/formatters';
    import { t } from '../lib/i18n';
    import SkillReview from './SkillReview.svelte';
    import {
        addPermissionRule,
        fetchActionSamples,
        fetchDurationProfile,
        fetchPermissionCandidates,
        fetchTokenAttribution,
        fetchTopActions,
        type ActionRow,
        type AttributionReport,
        type PermissionCandidate,
        type SignatureDuration,
        type SignatureStat,
    } from '../stores/experience';

    const dispatch = createEventDispatcher();

    // This modal gains more tabs as later LN items land — one modal, not a
    // new one per tab (see LEARN-TASKS.md LN-03 "UI").
    type Tab = 'actions' | 'permissions' | 'timing' | 'cost' | 'skills';
    let tab: Tab = 'actions';
    let skillReview: SkillReview;

    type SortKey = 'sig' | 'count' | 'runs' | 'errors' | 'tokens' | 'last';

    let project = '';
    let days = 30;
    const dayOptions: { id: number; key: string }[] = [
        { id: 1, key: 'experiencePanel.period.today' },
        { id: 7, key: 'experiencePanel.period.7days' },
        { id: 30, key: 'experiencePanel.period.30days' },
        { id: 90, key: 'experiencePanel.period.90days' },
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

    // Permissions tab (LEARN-TASKS.md LN-04).
    let permSafe: PermissionCandidate[] = [];
    let permNeedsReview: PermissionCandidate[] = [];
    let permLoading = true;
    let permError = '';
    // Which session an "Add rule" click targets, per candidate row (keyed by
    // "tool\0pattern") — defaults to the project's first configured session.
    let permSessionByRow: Record<string, string> = {};
    let permAddedRows: Set<string> = new Set();
    let permAddErrorByRow: Record<string, string> = {};

    // Timing tab (LEARN-TASKS.md LN-18) — no day-range picker: the profile
    // always aggregates DurationProfile's own fixed window, same window the
    // context primer's timing section reads.
    let durations: SignatureDuration[] = [];
    let durLoading = true;
    let durError = '';

    // Cost by tool tab (LEARN-TASKS.md LN-12) — same fixed-window aggregation
    // as Timing, no day-range picker.
    let attribution: AttributionReport = { TotalEstTokens: 0, BySignature: [], ByTool: [] };
    let costLoading = true;
    let costError = '';

    function rowKey(c: PermissionCandidate): string {
        return `${c.Tool}\0${c.Pattern}`;
    }

    $: projectSessions = $projects.find((p) => p.name === project)?.sessions ?? [];

    async function loadPermissions() {
        if (!project) {
            permSafe = [];
            permNeedsReview = [];
            permLoading = false;
            return;
        }
        permLoading = true;
        permError = '';
        try {
            const set = await fetchPermissionCandidates(project, days);
            permSafe = set.Safe ?? [];
            permNeedsReview = set.NeedsReview ?? [];
        } catch (e: any) {
            permError = $t('experiencePanel.permissions.errorLoad', { error: e?.message ?? String(e) });
            permSafe = [];
            permNeedsReview = [];
        } finally {
            permLoading = false;
        }
    }

    async function loadDurations() {
        if (!project) {
            durations = [];
            durLoading = false;
            return;
        }
        durLoading = true;
        durError = '';
        try {
            durations = await fetchDurationProfile(project);
        } catch (e: any) {
            durError = $t('experiencePanel.timing.errorLoad', { error: e?.message ?? String(e) });
            durations = [];
        } finally {
            durLoading = false;
        }
    }

    async function loadAttribution() {
        if (!project) {
            attribution = { TotalEstTokens: 0, BySignature: [], ByTool: [] };
            costLoading = false;
            return;
        }
        costLoading = true;
        costError = '';
        try {
            attribution = await fetchTokenAttribution(project, 30);
        } catch (e: any) {
            costError = $t('experiencePanel.cost.errorLoad', { error: e?.message ?? String(e) });
            attribution = { TotalEstTokens: 0, BySignature: [], ByTool: [] };
        } finally {
            costLoading = false;
        }
    }

    async function addRule(c: PermissionCandidate) {
        const key = rowKey(c);
        const sessionName = permSessionByRow[key] || projectSessions[0]?.name;
        if (!sessionName) {
            permAddErrorByRow = { ...permAddErrorByRow, [key]: $t('experiencePanel.permissions.noSession') };
            return;
        }
        permAddErrorByRow = { ...permAddErrorByRow, [key]: '' };
        try {
            await addPermissionRule(project, sessionName, c.Tool, c.Pattern, 'allow');
            permAddedRows = new Set(permAddedRows).add(key);
        } catch (e: any) {
            permAddErrorByRow = { ...permAddErrorByRow, [key]: e?.message ?? String(e) };
        }
    }

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
            error = $t('experiencePanel.actions.errorLoad', { error: e?.message ?? String(e) });
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
        loadPermissions();
        loadDurations();
        loadAttribution();
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
                <h2 class="text-text font-semibold text-base">{$t('experiencePanel.title')}</h2>
                <div class="flex items-center gap-1 text-xs">
                    <button
                        type="button"
                        on:click={() => (tab = 'actions')}
                        class="px-2 py-1 rounded border
                            {tab === 'actions'
                                ? 'bg-bg-elevated border-blue-500 text-text'
                                : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                        {$t('experiencePanel.tabs.actions')}
                    </button>
                    <button
                        type="button"
                        on:click={() => (tab = 'permissions')}
                        class="px-2 py-1 rounded border
                            {tab === 'permissions'
                                ? 'bg-bg-elevated border-blue-500 text-text'
                                : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                        {$t('experiencePanel.tabs.permissions')}
                    </button>
                    <button
                        type="button"
                        on:click={() => (tab = 'timing')}
                        class="px-2 py-1 rounded border
                            {tab === 'timing'
                                ? 'bg-bg-elevated border-blue-500 text-text'
                                : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                        {$t('experiencePanel.tabs.timing')}
                    </button>
                    <button
                        type="button"
                        on:click={() => (tab = 'cost')}
                        class="px-2 py-1 rounded border
                            {tab === 'cost'
                                ? 'bg-bg-elevated border-blue-500 text-text'
                                : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                        {$t('experiencePanel.tabs.cost')}
                    </button>
                    <button
                        type="button"
                        on:click={() => (tab = 'skills')}
                        class="px-2 py-1 rounded border
                            {tab === 'skills'
                                ? 'bg-bg-elevated border-blue-500 text-text'
                                : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                        {$t('experiencePanel.tabs.skills')}
                    </button>
                </div>
            </div>
            <div class="flex items-center gap-2">
                <button
                    type="button"
                    on:click={() => {
                        if (tab === 'actions') load();
                        else if (tab === 'permissions') loadPermissions();
                        else if (tab === 'timing') loadDurations();
                        else if (tab === 'cost') loadAttribution();
                        else skillReview?.load();
                    }}
                    class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg">
                    {$t('experiencePanel.refresh')}
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
                {$t('experiencePanel.filters.project')}
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
                        {$t(opt.key)}
                    </button>
                {/each}
            </div>
        </div>

        <!-- Body -->
        <div class="flex-1 min-h-0 overflow-y-auto">
            {#if tab === 'skills'}
                <SkillReview bind:this={skillReview} {project} />
            {:else if tab === 'permissions'}
                {#if !project}
                    <div class="text-text-muted text-sm italic py-10 text-center">
                        {$t('experiencePanel.noProject')}
                    </div>
                {:else if permLoading}
                    <div class="text-text-muted text-sm italic py-10 text-center">
                        {$t('experiencePanel.permissions.loading')}
                    </div>
                {:else if permError}
                    <div class="text-status-error text-sm py-10 text-center">{permError}</div>
                {:else if permSafe.length === 0 && permNeedsReview.length === 0}
                    <div class="text-text-muted text-sm italic py-10 text-center">
                        {$t('experiencePanel.permissions.emptyPre')}
                        <code class="font-mono">[optimization] experience_tracking</code>
                        {$t('experiencePanel.enable.collectThemSuffix')}
                    </div>
                {:else}
                    <div class="px-4 py-3">
                        {#if permSafe.length > 0}
                            <h3 class="text-text text-sm font-medium mb-2">
                                {$t('experiencePanel.permissions.safeHeading', { count: permSafe.length })}
                            </h3>
                            <table class="w-full text-sm border-collapse mb-6">
                                <thead class="bg-bg-elevated text-text-muted text-xs">
                                    <tr>
                                        <th class="text-left px-3 py-2 font-medium">{$t('experiencePanel.permissions.colTool')}</th>
                                        <th class="text-left px-3 py-2 font-medium">{$t('experiencePanel.permissions.colPattern')}</th>
                                        <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.permissions.colAsked')}</th>
                                        <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.permissions.colAllowed')}</th>
                                        <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.permissions.colDenied')}</th>
                                        <th class="text-left px-3 py-2 font-medium">{$t('experiencePanel.permissions.colSession')}</th>
                                        <th class="text-left px-3 py-2 font-medium"></th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {#each permSafe as c (rowKey(c))}
                                        {@const key = rowKey(c)}
                                        <tr class="border-t border-bg-border">
                                            <td class="px-3 py-1.5 text-text font-mono text-xs">{c.Tool}</td>
                                            <td class="px-3 py-1.5 text-text font-mono text-xs break-all">
                                                {c.Pattern}
                                            </td>
                                            <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                                {c.Count}
                                            </td>
                                            <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                                {c.AllowCount}
                                            </td>
                                            <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                                {c.DenyCount}
                                            </td>
                                            <td class="px-3 py-1.5">
                                                <select
                                                    bind:value={permSessionByRow[key]}
                                                    class="bg-bg border border-bg-border rounded px-1 py-0.5 text-xs text-text">
                                                    {#each projectSessions as s (s.name)}
                                                        <option value={s.name}>{s.name}</option>
                                                    {/each}
                                                </select>
                                            </td>
                                            <td class="px-3 py-1.5">
                                                {#if permAddedRows.has(key)}
                                                    <span class="text-status-working text-xs">{$t('experiencePanel.permissions.added')}</span>
                                                {:else}
                                                    <button
                                                        type="button"
                                                        on:click={() => addRule(c)}
                                                        class="px-2 py-0.5 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg">
                                                        {$t('experiencePanel.permissions.addRule')}
                                                    </button>
                                                {/if}
                                                {#if permAddErrorByRow[key]}
                                                    <div class="text-status-error text-xs mt-1">
                                                        {permAddErrorByRow[key]}
                                                    </div>
                                                {/if}
                                            </td>
                                        </tr>
                                    {/each}
                                </tbody>
                            </table>
                        {/if}

                        {#if permNeedsReview.length > 0}
                            <h3 class="text-text text-sm font-medium mb-2">
                                {$t('experiencePanel.permissions.needsReviewHeading', { count: permNeedsReview.length })}
                            </h3>
                            <table class="w-full text-sm border-collapse">
                                <thead class="bg-bg-elevated text-text-muted text-xs">
                                    <tr>
                                        <th class="text-left px-3 py-2 font-medium">{$t('experiencePanel.permissions.colTool')}</th>
                                        <th class="text-left px-3 py-2 font-medium">{$t('experiencePanel.permissions.colPattern')}</th>
                                        <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.permissions.colAsked')}</th>
                                        <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.permissions.colAllowed')}</th>
                                        <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.permissions.colDenied')}</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {#each permNeedsReview as c (rowKey(c))}
                                        <tr class="border-t border-bg-border">
                                            <td class="px-3 py-1.5 text-text font-mono text-xs">{c.Tool}</td>
                                            <td class="px-3 py-1.5 text-text font-mono text-xs break-all">
                                                {c.Pattern}
                                            </td>
                                            <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                                {c.Count}
                                            </td>
                                            <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                                {c.AllowCount}
                                            </td>
                                            <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                                {c.DenyCount}
                                            </td>
                                        </tr>
                                    {/each}
                                </tbody>
                            </table>
                        {/if}
                    </div>
                {/if}
            {:else if tab === 'timing'}
                {#if !project}
                    <div class="text-text-muted text-sm italic py-10 text-center">
                        {$t('experiencePanel.noProject')}
                    </div>
                {:else if durLoading}
                    <div class="text-text-muted text-sm italic py-10 text-center">
                        {$t('experiencePanel.timing.loading')}
                    </div>
                {:else if durError}
                    <div class="text-status-error text-sm py-10 text-center">{durError}</div>
                {:else if durations.length === 0}
                    <div class="text-text-muted text-sm italic py-10 text-center">
                        {$t('experiencePanel.timing.emptyPre')}
                        <code class="font-mono">[optimization] experience_tracking</code>
                        {$t('experiencePanel.enable.collectThemSuffix')}
                    </div>
                {:else}
                    <table class="w-full text-sm border-collapse">
                        <thead class="bg-bg-elevated sticky top-0 z-10 text-text-muted text-xs">
                            <tr>
                                <th class="text-left px-3 py-2 font-medium">{$t('experiencePanel.timing.colSignature')}</th>
                                <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.timing.colN')}</th>
                                <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.timing.colMedian')}</th>
                                <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.timing.colP90')}</th>
                                <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.timing.colMax')}</th>
                                <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.timing.colTotal')}</th>
                                <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.timing.colFailRate')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            {#each durations as row (row.Sig)}
                                <tr class="border-t border-bg-border">
                                    <td class="px-3 py-1.5 text-text font-mono text-xs break-all">
                                        {row.Sig}
                                    </td>
                                    <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                        {row.Count}
                                    </td>
                                    <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                        {formatDuration(row.MedianSec * 1000)}
                                    </td>
                                    <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                        {formatDuration(row.P90Sec * 1000)}
                                    </td>
                                    <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                        {formatDuration(row.MaxSec * 1000)}
                                    </td>
                                    <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                        {formatDuration(row.TotalSec * 1000)}
                                    </td>
                                    <td
                                        class="px-3 py-1.5 text-right font-mono text-xs
                                            {row.FailRate > 0 ? 'text-status-error' : 'text-text-muted'}">
                                        {formatPercent(row.FailRate)}
                                    </td>
                                </tr>
                            {/each}
                        </tbody>
                    </table>
                {/if}
            {:else if tab === 'cost'}
                {#if !project}
                    <div class="text-text-muted text-sm italic py-10 text-center">
                        {$t('experiencePanel.noProject')}
                    </div>
                {:else if costLoading}
                    <div class="text-text-muted text-sm italic py-10 text-center">
                        {$t('experiencePanel.cost.loading')}
                    </div>
                {:else if costError}
                    <div class="text-status-error text-sm py-10 text-center">{costError}</div>
                {:else if attribution.BySignature.length === 0}
                    <div class="text-text-muted text-sm italic py-10 text-center">
                        {$t('experiencePanel.cost.emptyPre')}
                        <code class="font-mono">[optimization] experience_tracking</code>
                        {$t('experiencePanel.enable.collectItSuffix')}
                    </div>
                {:else}
                    <div class="px-4 py-3">
                        <h3 class="text-text text-sm font-medium mb-2">
                            {$t('experiencePanel.cost.byToolHeading', { tokens: formatTokens(attribution.TotalEstTokens) })}
                        </h3>
                        <table class="w-full text-sm border-collapse mb-6">
                            <thead class="bg-bg-elevated text-text-muted text-xs">
                                <tr>
                                    <th class="text-left px-3 py-2 font-medium">{$t('experiencePanel.cost.colTool')}</th>
                                    <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.cost.colCalls')}</th>
                                    <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.cost.colEstTokens')}</th>
                                    <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.cost.colShare')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {#each attribution.ByTool as t (t.Tool)}
                                    <tr class="border-t border-bg-border">
                                        <td class="px-3 py-1.5 text-text font-mono text-xs">{t.Tool}</td>
                                        <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                            {t.Count}
                                        </td>
                                        <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                            {formatTokens(t.EstTokens)}
                                        </td>
                                        <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                            {formatPercent(t.Share)}
                                        </td>
                                    </tr>
                                {/each}
                            </tbody>
                        </table>

                        <h3 class="text-text text-sm font-medium mb-2">
                            {$t('experiencePanel.cost.topSignaturesHeading')}
                        </h3>
                        <table class="w-full text-sm border-collapse">
                            <thead class="bg-bg-elevated text-text-muted text-xs">
                                <tr>
                                    <th class="text-left px-3 py-2 font-medium">{$t('experiencePanel.cost.colSignature')}</th>
                                    <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.cost.colCalls')}</th>
                                    <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.cost.colEstTokens')}</th>
                                    <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.cost.colShare')}</th>
                                    <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.cost.colAvgChars')}</th>
                                    <th class="text-right px-3 py-2 font-medium">{$t('experiencePanel.cost.colMaxChars')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {#each attribution.BySignature as row (row.Sig)}
                                    <tr class="border-t border-bg-border">
                                        <td class="px-3 py-1.5 text-text font-mono text-xs break-all">
                                            {row.Sig}
                                        </td>
                                        <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                            {row.Count}
                                        </td>
                                        <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                            {formatTokens(row.EstTokens)}
                                        </td>
                                        <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                            {formatPercent(row.Share)}
                                        </td>
                                        <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                            {Math.round(row.AvgResultChars)}
                                        </td>
                                        <td class="px-3 py-1.5 text-right text-text-muted font-mono text-xs">
                                            {row.MaxResultChars}
                                        </td>
                                    </tr>
                                {/each}
                            </tbody>
                        </table>
                    </div>
                {/if}
            {:else if !project}
                <div class="text-text-muted text-sm italic py-10 text-center">
                    {$t('experiencePanel.noProject')}
                </div>
            {:else if loading}
                <div class="text-text-muted text-sm italic py-10 text-center">
                    {$t('experiencePanel.actions.loading')}
                </div>
            {:else if error}
                <div class="text-status-error text-sm py-10 text-center">{error}</div>
            {:else if sorted.length === 0}
                <div class="text-text-muted text-sm italic py-10 text-center">
                    {$t('experiencePanel.actions.emptyPre')}
                    <code class="font-mono">[optimization] experience_tracking</code>
                    {$t('experiencePanel.enable.collectThemSuffix')}
                </div>
            {:else}
                <table class="w-full text-sm border-collapse">
                    <thead class="bg-bg-elevated sticky top-0 z-10 text-text-muted text-xs">
                        <tr>
                            <th class="w-6"></th>
                            <th
                                class="text-left px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('sig')}>
                                {$t('experiencePanel.actions.colSignature')}{sortIndicator('sig')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('count')}>
                                {$t('experiencePanel.actions.colN')}{sortIndicator('count')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('runs')}>
                                {$t('experiencePanel.actions.colRuns')}{sortIndicator('runs')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('errors')}>
                                {$t('experiencePanel.actions.colErrors')}{sortIndicator('errors')}
                            </th>
                            <th
                                class="text-right px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('tokens')}>
                                {$t('experiencePanel.actions.colOutTokens')}{sortIndicator('tokens')}
                            </th>
                            <th
                                class="text-left px-3 py-2 font-medium cursor-pointer select-none hover:text-text"
                                on:click={() => toggleSort('last')}>
                                {$t('experiencePanel.actions.colLastSeen')}{sortIndicator('last')}
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
                                                    {$t('experiencePanel.actions.sampleArgs')}
                                                    {#each row.SampleArgs ?? [] as a, i (i)}
                                                        <span class="font-mono text-text-muted">{a}</span>{i < (row.SampleArgs?.length ?? 0) - 1 ? ', ' : ''}
                                                    {/each}
                                                </div>
                                            {/if}
                                            <div class="text-text-muted text-xs mb-1">{$t('experiencePanel.actions.examples')}</div>
                                            {#if samplesLoadingBySig[row.Sig]}
                                                <div class="text-text-muted italic text-xs py-2">{$t('experiencePanel.actions.loadingExamples')}</div>
                                            {:else if samplesErrorBySig[row.Sig]}
                                                <div class="text-status-error text-xs py-2 font-mono break-words">
                                                    {samplesErrorBySig[row.Sig]}
                                                </div>
                                            {:else if (samplesBySig[row.Sig] ?? []).length === 0}
                                                <div class="text-text-dim italic text-xs py-2">{$t('experiencePanel.actions.noSamples')}</div>
                                            {:else}
                                                <div class="max-h-[240px] overflow-y-auto bg-bg border border-bg-border rounded font-mono text-[12px] leading-5 px-2 py-1">
                                                    {#each samplesBySig[row.Sig] ?? [] as s (s.ID)}
                                                        <div class="flex items-start gap-2 py-px {s.IsError ? 'text-status-error' : 'text-text'}">
                                                            <span class="text-text-dim shrink-0 select-none">
                                                                [{formatTime(s.Timestamp)}]
                                                            </span>
                                                            <span class="text-text-muted shrink-0">{s.Session}</span>
                                                            <span class="whitespace-pre-wrap break-words">
                                                                {s.Arg || $t('experiencePanel.actions.noArg')}
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
            {#if tab === 'actions'}
                {$t('experiencePanel.footer.actions')}
            {:else if tab === 'timing'}
                {$t('experiencePanel.footer.timing')}
            {:else if tab === 'cost'}
                {$t('experiencePanel.footer.cost')}
            {:else if tab === 'skills'}
                {$t('experiencePanel.footer.skills')}
            {/if}
        </div>
    </div>
</div>
