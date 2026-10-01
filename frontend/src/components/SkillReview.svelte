<script lang="ts">
    // The "Skills" tab body (LEARN-TASKS.md LN-10, autopilot LN-24..27):
    // the skill autopilot switch, the project's skill library — every skill
    // with its status, how much agents load it, its measured effect and why
    // it is where it is — and, below, the mined candidates for distilling by
    // hand. Extracted out of ExperiencePanel (mounted as
    // <SkillReview {project} />) so this surface doesn't balloon that file.
    import { get } from 'svelte/store';
    import { onDestroy, onMount, tick } from 'svelte';
    import { formatCost, formatPercent, formatTokens } from '../lib/formatters';
    import { renderMarkdown } from '../lib/markdown';
    import { t } from '../lib/i18n';
    import { MODELS } from '../lib/models';
    import { GetConfig } from '../../wailsjs/go/main/App';
    import { EventsOn } from '../../wailsjs/runtime/runtime';
    import {
        approveSkill,
        archiveSkill,
        distillSkill,
        fetchSkillAutopilot,
        fetchSkillCandidates,
        fetchSkillQuality,
        fetchSkillUsage,
        fetchSkills,
        parseSkillDraft,
        parseSkillReason,
        restoreSkill,
        runSkillAutopilot,
        setSkillAutopilot,
        type Skill,
        type SkillAutopilotState,
        type SkillCandidate,
        type SkillEffect,
        type SkillPilotReport,
        type SkillReason,
        type SkillStatus,
        type SkillUsage,
    } from '../stores/experience';

    export let project = '';

    let skills: Skill[] = [];
    let loading = true;
    let error = '';

    // Usage (LN-24) and before/after effect (LN-11) per skill — each loaded
    // alongside the list; a failure in either must not hide the list itself.
    let usageById: Record<number, SkillUsage> = {};
    let effectById: Record<number, SkillEffect> = {};
    let usageError = '';

    // Skill autopilot (LN-27) header state.
    let autopilot: SkillAutopilotState | null = null;
    let autopilotBusy = false;
    let autopilotError = '';
    let budgetInput = 0;
    let lastRun: SkillPilotReport | null = null;

    // Candidates mined from action history (LEARN-TASKS.md LN-08) — the only
    // source of an experience.SkillCandidate to hand DistillSkill (LN-09).
    // gates comes from the project's own config (the distiller passes them
    // through to the drafted skill's own gate list).
    let candidates: SkillCandidate[] = [];
    let candidatesError = '';
    let candidatesOpen: boolean | null = null; // null = follow the autopilot
    let gates: string[] = [];
    let distillModel = 'sonnet';
    // 0 = automatic (relative threshold over the project's own current
    // candidates, LEARN-TASKS.md LN-23); >0 overrides it outright.
    let distillMinScore = 0;
    let distillBusyKey: string | null = null;
    let distillErrorByKey: Record<string, string> = {};
    // Last activity line from the analyst CLI while a distillation is in
    // flight (skill:progress) — without this the busy button is the only
    // sign of life for a call that can genuinely take a minute or more.
    let distillProgress = '';
    let unsubs: (() => void)[] = [];

    onMount(() => {
        unsubs.push(
            EventsOn('skill:progress', (evt: { project: string; text: string }) => {
                if (evt.project !== project) return;
                distillProgress = evt.text;
            }),
            // The autopilot runs after finished runs, in the background —
            // reload whatever it changed while this tab is open.
            EventsOn('skills:changed', (evt: { project: string }) => {
                if (evt.project === project) load();
            }),
        );
    });
    onDestroy(() => unsubs.forEach((u) => u()));

    function candidateKey(c: SkillCandidate): string {
        return c.Sig.join('\x1f');
    }

    // A candidate stays in the mined list even after it's been distilled, so
    // the row alone never says "already done". SourceJSON is the candidate's
    // Sig encoded by Go's json.Marshal, which escapes <, > and & — unlike
    // JSON.stringify — so both sides are normalized through parse +
    // stringify. An archived skill never shadows a live one for the same
    // sequence.
    function sigKey(json: string): string {
        try {
            return JSON.stringify(JSON.parse(json));
        } catch {
            return json;
        }
    }
    $: distilledNameBySig = skills.reduce<Record<string, { id: number; name: string; status: string }>>(
        (acc, sk) => {
            const k = sigKey(sk.SourceJSON);
            if (sk.Status === 'archived' && acc[k]) return acc;
            acc[k] = { id: sk.ID, name: sk.Name, status: sk.Status };
            return acc;
        },
        {},
    );
    $: alreadyDistilled = (c: SkillCandidate) => distilledNameBySig[sigKey(JSON.stringify(c.Sig))];

    // A candidate's Kind (experience.CandidateKind) says what it is actually
    // worth turning into — the backend already ranks skill-kind candidates
    // first; this filter is for hiding the rest outright.
    let skillKindOnly = false;
    $: visibleCandidates = skillKindOnly
        ? candidates.filter((c) => c.Kind === 'skill')
        : candidates;

    const KIND_KEYS: Record<string, string> = {
        skill: 'skillReview.kindSkill',
        permission: 'skillReview.kindPermission',
        noise: 'skillReview.kindNoise',
    };
    const REASON_KEYS: Record<string, string> = {
        multi_step: 'skillReview.reasonMultiStep',
        known_failure: 'skillReview.reasonKnownFailure',
        read_only: 'skillReview.reasonReadOnly',
        loop: 'skillReview.reasonLoop',
        single_step: 'skillReview.reasonSingleStep',
    };

    // Reactive assignments, not plain functions: the template calls these
    // with an argument, so Svelte would not otherwise see $t as a dependency
    // and the cells would keep the old locale after a language switch.
    $: kindLabel = (c: SkillCandidate): string =>
        KIND_KEYS[c.Kind] ? $t(KIND_KEYS[c.Kind]) : c.Kind ?? '';
    $: kindReasonLabel = (c: SkillCandidate): string =>
        REASON_KEYS[c.KindReason] ? $t(REASON_KEYS[c.KindReason]) : c.KindReason ?? '';

    // Row expansion: clicking a skill opens its review/edit panel. Working
    // copies of the markdown body are kept separately from the loaded row so
    // an in-progress edit survives collapsing/re-expanding within one load().
    let expandedId: number | null = null;
    let editedMdById: Record<number, string> = {};
    let previewById: Record<number, boolean> = {};
    let busyId: number | null = null;
    // conflict: the target .claude/skills/<name> already exists — which
    // action ran into it, so "overwrite" repeats that action.
    let conflict: { id: number; action: 'approve' | 'restore' } | null = null;
    let errorById: Record<number, string> = {};

    // Library filter.
    type Filter = 'all' | 'live' | 'draft' | 'archived' | 'rejected';
    const FILTERS: Filter[] = ['all', 'live', 'draft', 'archived', 'rejected'];
    let filter: Filter = 'all';
    const FILTER_STATUSES: Record<Filter, SkillStatus[] | null> = {
        all: null,
        live: ['trial', 'approved'],
        draft: ['draft'],
        archived: ['archived'],
        rejected: ['rejected'],
    };
    $: countBy = (f: Filter) =>
        FILTER_STATUSES[f] ? skills.filter((s) => FILTER_STATUSES[f]!.includes(s.Status)).length : skills.length;
    // Live skills first, then drafts, then the history (archived/rejected).
    const STATUS_ORDER: Record<string, number> = { trial: 0, approved: 1, draft: 2, archived: 3, rejected: 4 };
    $: visibleSkills = skills
        .filter((s) => !FILTER_STATUSES[filter] || FILTER_STATUSES[filter]!.includes(s.Status))
        .slice()
        .sort((a, b) => (STATUS_ORDER[a.Status] ?? 9) - (STATUS_ORDER[b.Status] ?? 9));

    export async function load() {
        if (!project) {
            skills = [];
            candidates = [];
            autopilot = null;
            loading = false;
            return;
        }
        loading = skills.length === 0;
        error = '';
        try {
            skills = await fetchSkills(project);
        } catch (e: any) {
            error = get(t)('skillReview.failedToLoadSkills', { message: e?.message ?? String(e) });
            skills = [];
        } finally {
            loading = false;
        }
        usageError = '';
        try {
            const [usage, quality] = await Promise.all([fetchSkillUsage(project), fetchSkillQuality(project)]);
            usageById = usage;
            effectById = Object.fromEntries(quality.map((q) => [q.skill_id, q]));
        } catch (e: any) {
            usageError = get(t)('skillReview.failedToLoadSkillQuality', { message: e?.message ?? String(e) });
            usageById = {};
            effectById = {};
        }
        try {
            autopilot = await fetchSkillAutopilot(project);
            budgetInput = autopilot.daily_budget_usd;
        } catch {
            autopilot = null;
        }
        candidatesError = '';
        try {
            candidates = await fetchSkillCandidates(project);
        } catch (e: any) {
            candidatesError = get(t)('skillReview.failedToLoadCandidates', { message: e?.message ?? String(e) });
            candidates = [];
        }
        try {
            const cfg = await GetConfig();
            gates = cfg.Projects?.find((p) => p.Name === project)?.Gates ?? [];
        } catch {
            gates = [];
        }
    }

    $: if (project) load();

    async function onToggleAutopilot() {
        if (!autopilot || autopilotBusy) return;
        autopilotBusy = true;
        autopilotError = '';
        try {
            await setSkillAutopilot(project, !autopilot.enabled, Math.max(0, Number(budgetInput) || 0));
            autopilot = await fetchSkillAutopilot(project);
        } catch (e: any) {
            autopilotError = e?.message ?? String(e);
        } finally {
            autopilotBusy = false;
        }
    }

    async function onSaveBudget() {
        if (!autopilot || autopilotBusy) return;
        const budget = Math.max(0, Number(budgetInput) || 0);
        if (budget === autopilot.daily_budget_usd) return;
        autopilotBusy = true;
        autopilotError = '';
        try {
            await setSkillAutopilot(project, autopilot.enabled, budget);
            autopilot = await fetchSkillAutopilot(project);
        } catch (e: any) {
            autopilotError = e?.message ?? String(e);
        } finally {
            autopilotBusy = false;
        }
    }

    async function onRunAutopilot() {
        if (autopilotBusy) return;
        autopilotBusy = true;
        autopilotError = '';
        lastRun = null;
        try {
            lastRun = await runSkillAutopilot(project);
            await load();
        } catch (e: any) {
            autopilotError = e?.message ?? String(e);
        } finally {
            autopilotBusy = false;
        }
    }

    $: runSummary = (r: SkillPilotReport): string => {
        const parts: string[] = [];
        if (r.applied?.length) parts.push($t('skillReview.runApplied', { names: r.applied.join(', ') }));
        if (r.kept?.length) parts.push($t('skillReview.runKept', { names: r.kept.join(', ') }));
        if (r.archived?.length) parts.push($t('skillReview.runArchived', { names: r.archived.join(', ') }));
        if (r.rejected?.length) parts.push($t('skillReview.runRejected', { names: r.rejected.join(', ') }));
        if (r.idle) parts.push($t(`skillReview.idle.${r.idle}`));
        return parts.join(' · ') || $t('skillReview.runNothing');
    };

    // Re-distilling a sequence that already has a skill costs another
    // analyst call and leaves a second draft behind, so it takes a second
    // click (window.confirm() is dead in WebView2): the first click arms the
    // button, the second within a few seconds runs it.
    let redistillArmedKey: string | null = null;
    let redistillTimer: ReturnType<typeof setTimeout> | null = null;

    function onRedistill(c: SkillCandidate) {
        const key = candidateKey(c);
        if (redistillArmedKey === key) {
            redistillArmedKey = null;
            if (redistillTimer) clearTimeout(redistillTimer);
            onDistill(c);
            return;
        }
        redistillArmedKey = key;
        if (redistillTimer) clearTimeout(redistillTimer);
        redistillTimer = setTimeout(() => (redistillArmedKey = null), 4000);
    }

    // Open a skill's review panel and bring its row into view.
    async function revealSkill(id: number) {
        const sk = skills.find((s) => s.ID === id);
        if (!sk) return;
        if (FILTER_STATUSES[filter] && !FILTER_STATUSES[filter]!.includes(sk.Status)) filter = 'all';
        expandedId = id;
        conflict = null;
        if (editedMdById[id] === undefined) editedMdById = { ...editedMdById, [id]: sk.MD };
        await tick();
        document.getElementById(`skill-row-${id}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }

    async function onDistill(c: SkillCandidate) {
        if (distillBusyKey !== null) return;
        const key = candidateKey(c);
        distillBusyKey = key;
        distillProgress = '';
        distillErrorByKey = { ...distillErrorByKey, [key]: '' };
        try {
            const sk = await distillSkill(project, c, gates, distillModel, distillMinScore);
            await load();
            if (sk?.ID) await revealSkill(sk.ID);
        } catch (e: any) {
            const msg = e?.message ?? String(e);
            distillErrorByKey = {
                ...distillErrorByKey,
                // The backend message already names both the candidate's
                // score and the threshold it missed (LEARN-TASKS.md LN-23) —
                // shown as-is rather than swapped for a generic phrase.
                [key]: /below distillation threshold/i.test(msg)
                    ? get(t)('skillReview.belowThreshold', { message: msg })
                    : get(t)('skillReview.distillFailed', { message: msg }),
            };
        } finally {
            distillBusyKey = null;
            distillProgress = '';
        }
    }

    function toggleExpand(sk: Skill) {
        if (expandedId === sk.ID) {
            expandedId = null;
            return;
        }
        expandedId = sk.ID;
        conflict = null;
        errorById = { ...errorById, [sk.ID]: '' };
        if (editedMdById[sk.ID] === undefined) {
            editedMdById = { ...editedMdById, [sk.ID]: sk.MD };
        }
    }

    async function runAction(sk: Skill, action: 'approve' | 'restore' | 'archive', overwrite = false) {
        if (busyId !== null) return;
        busyId = sk.ID;
        errorById = { ...errorById, [sk.ID]: '' };
        try {
            if (action === 'approve') await approveSkill(sk.ID, editedMdById[sk.ID] ?? sk.MD, overwrite);
            else if (action === 'restore') await restoreSkill(sk.ID, overwrite);
            else await archiveSkill(sk.ID);
            conflict = null;
            await load();
        } catch (e: any) {
            const msg = e?.message ?? String(e);
            if (action !== 'archive' && !overwrite && /already exists/i.test(msg)) {
                conflict = { id: sk.ID, action };
            } else {
                errorById = { ...errorById, [sk.ID]: msg };
            }
        } finally {
            busyId = null;
        }
    }

    const STATUS_KEYS: Record<string, string> = {
        draft: 'skillReview.statusDraft',
        rejected: 'skillReview.statusRejected',
        trial: 'skillReview.statusTrial',
        approved: 'skillReview.statusApproved',
        archived: 'skillReview.statusArchived',
    };
    const STATUS_CLASSES: Record<string, string> = {
        draft: 'bg-status-waiting/15 text-status-waiting',
        rejected: 'bg-bg-elevated text-text-muted',
        trial: 'bg-blue-500/15 text-blue-400',
        approved: 'bg-status-working/15 text-status-working',
        archived: 'bg-bg-elevated text-text-muted',
    };

    function statusLabel(status: string, tr: (key: string, params?: Record<string, string | number>) => string): string {
        return STATUS_KEYS[status] ? tr(STATUS_KEYS[status]) : status;
    }

    // Why a row is in its status, in words (experience.SkillReason).
    $: reasonLabel = (sk: Skill): string => {
        const r: SkillReason | null = parseSkillReason(sk.Reason);
        if (!r) return '';
        const key = `skillReview.why.${r.code}`;
        const text = $t(key, {
            text: r.text ?? '',
            runs: r.runs ?? 0,
            loads: r.loads ?? 0,
            before: formatTokens(r.before ?? 0),
            after: formatTokens(r.after ?? 0),
            name: r.duplicate_of ?? sk.Name,
        });
        if (text === key) return r.text ?? r.code;
        return r.code === 'review_rejected' && r.duplicate_of
            ? `${text} ${$t('skillReview.why.duplicateOf', { name: r.duplicate_of })}`
            : text;
    };

    function formatDate(s: string | null | undefined): string {
        if (!s) return '';
        const d = new Date(s);
        if (isNaN(d.getTime())) return '';
        const dd = String(d.getDate()).padStart(2, '0');
        const mo = String(d.getMonth() + 1).padStart(2, '0');
        const hh = String(d.getHours()).padStart(2, '0');
        const mi = String(d.getMinutes()).padStart(2, '0');
        return `${dd}.${mo} ${hh}:${mi}`;
    }

    // Effect cell: tokens before → after, coloured by direction.
    function effectClass(e: SkillEffect): string {
        if (e.after.median_input_tokens < e.before.median_input_tokens) return 'text-status-working';
        if (e.after.median_input_tokens > e.before.median_input_tokens) return 'text-status-error';
        return 'text-text-muted';
    }

    $: showCandidates = candidatesOpen ?? !autopilot?.enabled;
</script>

{#if !project}
    <div class="text-text-muted text-sm italic py-10 text-center">{$t('skillReview.noProjectConfigured')}</div>
{:else if loading}
    <div class="text-text-muted text-sm italic py-10 text-center">{$t('skillReview.loadingSkills')}</div>
{:else if error}
    <div class="text-status-error text-sm py-10 text-center">{error}</div>
{:else}
    <!-- Autopilot (LN-27) -->
    {#if autopilot}
        <div class="m-3 rounded border px-3 py-2.5
                    {autopilot.enabled ? 'border-status-working/50 bg-status-working/5' : 'border-bg-border bg-bg-elevated/40'}"
             data-testid="skill-autopilot">
            <div class="flex items-center gap-3 flex-wrap">
                <button
                    type="button"
                    role="switch"
                    aria-checked={autopilot.enabled}
                    disabled={autopilotBusy}
                    on:click={onToggleAutopilot}
                    class="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors disabled:opacity-50
                           {autopilot.enabled ? 'bg-status-working' : 'bg-bg-border'}">
                    <span class="inline-block h-4 w-4 rounded-full bg-white transition-transform
                                 {autopilot.enabled ? 'translate-x-4' : 'translate-x-0.5'}"></span>
                </button>
                <div class="text-sm font-medium text-text">{$t('skillReview.autopilotTitle')}</div>
                <span class="text-xs {autopilot.enabled ? 'text-status-working' : 'text-text-muted'}">
                    {autopilot.enabled ? $t('skillReview.autopilotOn') : $t('skillReview.autopilotOff')}
                </span>
                <div class="flex-1"></div>
                <label class="flex items-center gap-1 text-xs text-text-muted" title={$t('skillReview.budgetHint')}>
                    {$t('skillReview.budgetLabel')}
                    <input
                        type="number"
                        min="0"
                        step="0.5"
                        bind:value={budgetInput}
                        on:change={onSaveBudget}
                        class="w-16 bg-bg border border-bg-border rounded px-1 py-0.5 text-text" />
                </label>
                <span class="text-xs text-text-muted font-mono" title={$t('skillReview.spentHint')}>
                    {$t('skillReview.spent', { today: formatCost(autopilot.spent_today_usd), total: formatCost(autopilot.spent_total_usd) })}
                </span>
                {#if autopilot.enabled}
                    <button
                        type="button"
                        disabled={autopilotBusy}
                        on:click={onRunAutopilot}
                        class="px-2 py-0.5 text-xs rounded border border-bg-border bg-bg-elevated text-text hover:bg-bg disabled:opacity-50">
                        {autopilotBusy ? $t('skillReview.running') : $t('skillReview.runNow')}
                    </button>
                {/if}
            </div>
            <div class="mt-1.5 text-xs text-text-muted leading-relaxed">
                {autopilot.enabled
                    ? $t('skillReview.autopilotExplainOn', { trial: autopilot.trial_runs })
                    : $t('skillReview.autopilotExplainOff')}
                {#if !autopilot.enabled && !autopilot.tracking}
                    <span class="text-status-waiting">{$t('skillReview.autopilotEnablesTracking')}</span>
                {/if}
            </div>
            {#if lastRun}
                <div class="mt-1 text-xs text-text">{$t('skillReview.lastRun')}: {runSummary(lastRun)}</div>
            {/if}
            {#if autopilotError}
                <div class="mt-1 text-xs text-status-error">{autopilotError}</div>
            {/if}
        </div>
    {/if}

    <!-- Library -->
    <div class="px-3 pt-1 pb-1 flex items-center gap-2 flex-wrap">
        <div class="text-xs font-medium text-text-muted mr-2">{$t('skillReview.libraryHeading')}</div>
        {#each FILTERS as f (f)}
            {@const n = countBy(f)}
            {#if f === 'all' || n > 0}
                <button
                    type="button"
                    on:click={() => (filter = f)}
                    class="px-2 py-0.5 text-xs rounded border
                           {filter === f ? 'border-blue-500 bg-bg-elevated text-text' : 'border-bg-border text-text-muted hover:text-text'}">
                    {$t(`skillReview.filter.${f}`)} <span class="opacity-70">{n}</span>
                </button>
            {/if}
        {/each}
    </div>
    {#if usageError}
        <div class="px-3 py-1 text-status-error text-xs">{usageError}</div>
    {/if}

    {#if skills.length === 0}
        <div class="text-text-muted text-sm italic py-6 text-center px-6">
            {autopilot?.enabled ? $t('skillReview.noSkillsYetAuto') : $t('skillReview.noSkillsYet')}
        </div>
    {:else}
        <table class="w-full text-sm border-collapse" data-testid="skill-library">
            <thead class="bg-bg-elevated sticky top-0 z-10 text-text-muted text-xs">
                <tr>
                    <th class="w-6"></th>
                    <th class="text-left px-3 py-2 font-medium">{$t('skillReview.colName')}</th>
                    <th class="text-left px-3 py-2 font-medium">{$t('skillReview.colStatus')}</th>
                    <th class="text-left px-3 py-2 font-medium" title={$t('skillReview.colUsageHint')}>{$t('skillReview.colUsage')}</th>
                    <th class="text-left px-3 py-2 font-medium" title={$t('skillReview.colEffectHint')}>{$t('skillReview.colEffect')}</th>
                    <th class="text-left px-3 py-2 font-medium">{$t('skillReview.colDecision')}</th>
                </tr>
            </thead>
            <tbody>
                {#each visibleSkills as sk (sk.ID)}
                    {@const expanded = expandedId === sk.ID}
                    {@const draft = parseSkillDraft(sk.DraftJSON)}
                    {@const u = usageById[sk.ID]}
                    {@const eff = effectById[sk.ID]}
                    {@const live = sk.Status === 'trial' || sk.Status === 'approved'}
                    <tr
                        id="skill-row-{sk.ID}"
                        class="border-t border-bg-border cursor-pointer align-top
                               {expanded ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/60'}
                               {live ? '' : 'opacity-80'}"
                        on:click={() => toggleExpand(sk)}>
                        <td class="px-2 py-1.5 text-text-muted text-xs text-center select-none">
                            {expanded ? '▼' : '▶'}
                        </td>
                        <td class="px-3 py-1.5 text-xs max-w-[280px]">
                            <div class="flex items-center gap-1.5">
                                <span class="text-text font-mono">{sk.Name}</span>
                                {#if sk.Origin === 'auto'}
                                    <span class="px-1 rounded bg-blue-500/15 text-blue-400 text-[10px]" title={$t('skillReview.originAutoHint')}>
                                        {$t('skillReview.originAuto')}
                                    </span>
                                {/if}
                            </div>
                            {#if draft?.description}
                                <div class="mt-0.5 text-text-muted">{draft.description}</div>
                            {/if}
                        </td>
                        <td class="px-3 py-1.5 text-xs whitespace-nowrap">
                            <span class="px-1.5 py-0.5 rounded font-medium {STATUS_CLASSES[sk.Status] ?? ''}">
                                {statusLabel(sk.Status, $t)}
                            </span>
                            {#if sk.Status === 'trial' && u && autopilot?.trial_runs}
                                <div class="mt-1 text-text-muted">
                                    {$t('skillReview.trialProgress', { runs: Math.min(u.runs_since_applied, autopilot.trial_runs), of: autopilot.trial_runs })}
                                </div>
                            {/if}
                            <div class="mt-1 text-text-muted font-mono">{formatDate(sk.UpdatedAt ?? sk.CreatedAt)}</div>
                        </td>
                        <td class="px-3 py-1.5 text-xs text-text-muted whitespace-nowrap">
                            {#if u && u.loads > 0}
                                <div class="text-text">{$t('skillReview.loads', { n: u.loads })}</div>
                                {#if sk.ApprovedAt && u.runs_since_applied > 0}
                                    <div>{$t('skillReview.inRuns', { n: u.runs_with_load, of: u.runs_since_applied })}</div>
                                {/if}
                                {#if u.last_loaded_at}
                                    <div class="font-mono">{$t('skillReview.lastLoaded', { at: formatDate(u.last_loaded_at) })}</div>
                                {/if}
                            {:else if live}
                                <span class="text-status-waiting">{$t('skillReview.neverLoaded')}</span>
                                {#if u && u.runs_since_applied > 0}
                                    <div>{$t('skillReview.inRuns', { n: 0, of: u.runs_since_applied })}</div>
                                {/if}
                            {:else}
                                —
                            {/if}
                        </td>
                        <td class="px-3 py-1.5 text-xs whitespace-nowrap">
                            {#if !eff}
                                <span class="text-text-muted">—</span>
                            {:else if eff.stale && live}
                                <span class="text-status-waiting">
                                    {eff.stale_reason === 'unused' ? $t('skillReview.suggestArchivingUnused') : $t('skillReview.suggestArchivingNoImprovement')}
                                </span>
                            {:else if eff.insufficient_data}
                                <span class="text-text-muted italic" title={$t('skillReview.effectRuns', { before: eff.before.runs, after: eff.after.runs })}>
                                    {$t('skillReview.notEnoughData')}
                                </span>
                            {:else}
                                <span class="font-mono {effectClass(eff)}"
                                      title={$t('skillReview.effectRuns', { before: eff.before.runs, after: eff.after.runs })}>
                                    {formatTokens(eff.before.median_input_tokens)} → {formatTokens(eff.after.median_input_tokens)}
                                </span>
                                <div class="text-text-muted">
                                    {formatPercent(eff.before.completed_rate)} → {formatPercent(eff.after.completed_rate)}
                                </div>
                            {/if}
                        </td>
                        <td class="px-3 py-1.5 text-xs text-text-muted max-w-[320px]">
                            {reasonLabel(sk) || '—'}
                            {#if sk.CostUSD > 0}
                                <span class="ml-1 font-mono opacity-70">{formatCost(sk.CostUSD)}</span>
                            {/if}
                        </td>
                    </tr>
                    {#if expanded}
                        <tr class="bg-bg">
                            <td colspan="6" class="px-0 py-0">
                                <div class="px-4 py-3 border-t border-b border-bg-border">
                                    <div class="flex items-center justify-between mb-2">
                                        <div class="flex items-center gap-1 text-xs">
                                            <button
                                                type="button"
                                                on:click|stopPropagation={() =>
                                                    (previewById = { ...previewById, [sk.ID]: false })}
                                                class="px-2 py-1 rounded border
                                                    {!previewById[sk.ID]
                                                        ? 'bg-bg-elevated border-blue-500 text-text'
                                                        : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                                                {$t('skillReview.editButton')}
                                            </button>
                                            <button
                                                type="button"
                                                on:click|stopPropagation={() =>
                                                    (previewById = { ...previewById, [sk.ID]: true })}
                                                class="px-2 py-1 rounded border
                                                    {previewById[sk.ID]
                                                        ? 'bg-bg-elevated border-blue-500 text-text'
                                                        : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                                                {$t('skillReview.previewButton')}
                                            </button>
                                        </div>
                                        <div class="flex items-center gap-2">
                                            {#if sk.Status === 'draft'}
                                                <button
                                                    type="button"
                                                    disabled={busyId === sk.ID}
                                                    on:click|stopPropagation={() => runAction(sk, 'archive')}
                                                    class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg disabled:opacity-50">
                                                    {$t('skillReview.archiveButton')}
                                                </button>
                                                <button
                                                    type="button"
                                                    disabled={busyId === sk.ID}
                                                    on:click|stopPropagation={() => runAction(sk, 'approve')}
                                                    class="px-2 py-1 text-xs rounded bg-status-working/80 hover:bg-status-working text-white disabled:opacity-50">
                                                    {$t('skillReview.acceptButton')}
                                                </button>
                                            {:else if live}
                                                <button
                                                    type="button"
                                                    disabled={busyId === sk.ID}
                                                    on:click|stopPropagation={() => runAction(sk, 'archive')}
                                                    class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg disabled:opacity-50">
                                                    {$t('skillReview.switchOffButton')}
                                                </button>
                                            {:else}
                                                <button
                                                    type="button"
                                                    disabled={busyId === sk.ID}
                                                    on:click|stopPropagation={() => runAction(sk, 'restore')}
                                                    class="px-2 py-1 text-xs rounded bg-status-working/80 hover:bg-status-working text-white disabled:opacity-50">
                                                    {sk.Status === 'rejected' ? $t('skillReview.applyAnywayButton') : $t('skillReview.restoreButton')}
                                                </button>
                                            {/if}
                                        </div>
                                    </div>

                                    {#if previewById[sk.ID]}
                                        <div
                                            class="md-body text-text text-sm bg-bg border border-bg-border rounded px-3 py-2 max-h-[360px] overflow-y-auto">
                                            {@html renderMarkdown(editedMdById[sk.ID] ?? sk.MD)}
                                        </div>
                                    {:else}
                                        <textarea
                                            bind:value={editedMdById[sk.ID]}
                                            readonly={sk.Status !== 'draft'}
                                            rows="16"
                                            class="w-full bg-bg border border-bg-border rounded px-3 py-2 text-xs font-mono text-text resize-y"
                                        ></textarea>
                                    {/if}

                                    {#if conflict?.id === sk.ID}
                                        <div class="mt-2 text-xs">
                                            <span class="text-status-waiting">
                                                {$t('skillReview.skillFileExists', { name: sk.Name })}
                                            </span>
                                            <button
                                                type="button"
                                                on:click|stopPropagation={() => conflict && runAction(sk, conflict.action, true)}
                                                disabled={busyId === sk.ID}
                                                class="ml-2 px-2 py-0.5 rounded bg-status-error/80 hover:bg-status-error text-white disabled:opacity-50">
                                                {$t('skillReview.yesOverwrite')}
                                            </button>
                                        </div>
                                    {:else if errorById[sk.ID]}
                                        <div class="mt-2 text-status-error text-xs">{errorById[sk.ID]}</div>
                                    {/if}
                                </div>
                            </td>
                        </tr>
                    {/if}
                {/each}
            </tbody>
        </table>
    {/if}

    <!-- Candidates (manual distillation) -->
    {#if candidatesError}
        <div class="px-3 py-2 text-status-error text-xs">{candidatesError}</div>
    {:else if candidates.length > 0}
        <div class="border-t border-bg-border mt-3">
            <div class="px-3 pt-2 pb-1 flex items-center justify-between">
                <button
                    type="button"
                    on:click={() => (candidatesOpen = !showCandidates)}
                    class="text-xs font-medium text-text-muted hover:text-text">
                    {showCandidates ? '▼' : '▶'} {$t('skillReview.candidatesHeading')} ({candidates.length})
                </button>
                {#if showCandidates}
                    <div class="flex items-center gap-3">
                        <label class="flex items-center gap-1 text-xs text-text-muted">
                            <input type="checkbox" bind:checked={skillKindOnly} />
                            {$t('skillReview.hideNonSkill')}
                        </label>
                        <label class="flex items-center gap-1 text-xs text-text-muted" title={$t('skillReview.minScoreHint')}>
                            {$t('skillReview.minScoreLabel')}
                            <input
                                type="number"
                                min="0"
                                step="0.1"
                                bind:value={distillMinScore}
                                placeholder="0"
                                class="w-16 bg-bg border border-bg-border rounded px-1 py-0.5 text-text" />
                        </label>
                        <label class="flex items-center gap-1 text-xs text-text-muted">
                            {$t('skillReview.modelLabel')}
                            <select
                                bind:value={distillModel}
                                class="bg-bg border border-bg-border rounded px-1 py-0.5 text-text">
                                {#each MODELS as m (m.value)}
                                    <option value={m.value}>{m.label}</option>
                                {/each}
                            </select>
                        </label>
                    </div>
                {/if}
            </div>
            {#if showCandidates}
                <table class="w-full text-xs border-collapse mb-2">
                    <thead class="text-text-muted">
                        <tr>
                            <th class="text-left px-3 py-1 font-medium">{$t('skillReview.colSequence')}</th>
                            <th class="text-left px-3 py-1 font-medium">{$t('skillReview.colKind')}</th>
                            <th class="text-right px-3 py-1 font-medium">{$t('skillReview.colRuns')}</th>
                            <th class="text-right px-3 py-1 font-medium">{$t('skillReview.colScore')}</th>
                            <th class="px-3 py-1"></th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each visibleCandidates as c (candidateKey(c))}
                            {@const key = candidateKey(c)}
                            {@const distilled = alreadyDistilled(c)}
                            <tr
                                class="border-t border-bg-border align-top
                                       {distilled?.status === 'approved' || distilled?.status === 'trial'
                                           ? 'bg-status-working/10'
                                           : distilled ? 'bg-status-waiting/10' : ''}">
                                <td class="px-3 py-1 text-text font-mono">
                                    {c.Sig.join(' → ')}
                                    {#if c.ContextLossSuspect}
                                        <span class="ml-1 text-status-waiting">({$t('skillReview.loopSuspect')})</span>
                                    {/if}
                                    {#if c.Imported}
                                        <span class="ml-1 text-text-muted italic">({$t('skillReview.imported')})</span>
                                    {/if}
                                    {#if distilled}
                                        <div class="mt-0.5 text-status-working font-sans">
                                            {$t('skillReview.alreadyDistilled', { name: distilled.name, status: statusLabel(distilled.status, $t) })}
                                        </div>
                                    {/if}
                                </td>
                                <td class="px-3 py-1 whitespace-nowrap" title={kindReasonLabel(c)}>
                                    <span class={c.Kind === 'skill' ? 'text-status-working' : 'text-text-muted'}>
                                        {kindLabel(c)}
                                    </span>
                                </td>
                                <td class="px-3 py-1 text-right text-text-muted font-mono whitespace-nowrap">
                                    {c.DistinctRuns} ({formatPercent(c.RunShare)})
                                </td>
                                <td class="px-3 py-1 text-right text-text-muted font-mono">{c.Score.toFixed(1)}</td>
                                <td class="px-3 py-1 text-right">
                                    {#if distilled && distillBusyKey !== key}
                                        <button
                                            type="button"
                                            on:click={() => revealSkill(distilled.id)}
                                            class="px-2 py-0.5 rounded border border-bg-border bg-bg-elevated text-text hover:bg-bg whitespace-nowrap">
                                            {distilled.status === 'draft' ? $t('skillReview.reviewDraft') : $t('skillReview.viewSkill')}
                                        </button>
                                        <div class="mt-1">
                                            <button
                                                type="button"
                                                disabled={distillBusyKey !== null}
                                                on:click={() => onRedistill(c)}
                                                class="text-text-muted hover:text-text underline decoration-dotted disabled:opacity-50 whitespace-nowrap">
                                                {redistillArmedKey === key ? $t('skillReview.redistillConfirm') : $t('skillReview.redistill')}
                                            </button>
                                        </div>
                                    {:else}
                                        <button
                                            type="button"
                                            disabled={distillBusyKey !== null}
                                            on:click={() => onDistill(c)}
                                            class="px-2 py-0.5 rounded bg-status-working/80 hover:bg-status-working text-white disabled:opacity-50 whitespace-nowrap">
                                            {distillBusyKey === key ? $t('skillReview.distilling') : $t('skillReview.distillButton')}
                                        </button>
                                    {/if}
                                    {#if distillBusyKey === key && distillProgress}
                                        <div class="mt-1 text-text-muted italic truncate max-w-[220px]" title={distillProgress}>
                                            {distillProgress}
                                        </div>
                                    {/if}
                                    {#if distillErrorByKey[key]}
                                        <div class="mt-1 text-status-error">{distillErrorByKey[key]}</div>
                                    {/if}
                                </td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            {/if}
        </div>
    {/if}
{/if}
