<script lang="ts">
    // The "Skills" tab body (LEARN-TASKS.md LN-10): review/edit/accept/archive
    // the drafts LN-09's distiller produced. Extracted out of ExperiencePanel
    // (mounted as <SkillReview {project} />) so the review/edit surface — which
    // is meaningfully bigger than the Actions/Permissions/Timing tabs' plain
    // tables — doesn't balloon that file.
    import { get } from 'svelte/store';
    import { formatPercent, formatTime, formatTokens } from '../lib/formatters';
    import { renderMarkdown } from '../lib/markdown';
    import { t } from '../lib/i18n';
    import { MODELS } from '../lib/models';
    import { GetConfig } from '../../wailsjs/go/main/App';
    import {
        approveSkill,
        archiveSkill,
        distillSkill,
        fetchSkillCandidates,
        fetchSkillQuality,
        fetchSkills,
        parseSkillDraft,
        type Skill,
        type SkillCandidate,
        type SkillEffect,
    } from '../stores/experience';

    export let project = '';

    let skills: Skill[] = [];
    let loading = true;
    let error = '';

    // Before/after-approval effect table (LEARN-TASKS.md LN-11). Loaded
    // alongside the draft/approved list; a failure here must not hide the
    // skills list itself, so it gets its own error slot.
    let quality: SkillEffect[] = [];
    let qualityError = '';

    // Candidates mined from action history (LEARN-TASKS.md LN-08) — the only
    // source of an experience.SkillCandidate to hand DistillSkill (LN-09),
    // so this is where a new skill starts its life on this tab. gates comes
    // from the project's own config (the distiller passes them through to
    // the drafted skill's own gate list).
    let candidates: SkillCandidate[] = [];
    let candidatesError = '';
    let gates: string[] = [];
    let distillModel = 'sonnet';
    let distillBusyKey: string | null = null;
    let distillErrorByKey: Record<string, string> = {};

    function candidateKey(c: SkillCandidate): string {
        return c.Sig.join('\x1f');
    }

    // Row expansion: clicking a skill opens its review/edit panel. Working
    // copies of the markdown body are kept separately from the loaded row so
    // an in-progress edit survives collapsing/re-expanding within one load().
    let expandedId: number | null = null;
    let editedMdById: Record<number, string> = {};
    let previewById: Record<number, boolean> = {};
    let busyId: number | null = null;
    let conflictId: number | null = null;
    let errorById: Record<number, string> = {};

    export async function load() {
        if (!project) {
            skills = [];
            quality = [];
            candidates = [];
            loading = false;
            return;
        }
        loading = true;
        error = '';
        try {
            skills = await fetchSkills(project);
        } catch (e: any) {
            error = get(t)('skillReview.failedToLoadSkills', { message: e?.message ?? String(e) });
            skills = [];
        } finally {
            loading = false;
        }
        qualityError = '';
        try {
            quality = await fetchSkillQuality(project);
        } catch (e: any) {
            qualityError = get(t)('skillReview.failedToLoadSkillQuality', { message: e?.message ?? String(e) });
            quality = [];
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

    async function onDistill(c: SkillCandidate) {
        if (distillBusyKey !== null) return;
        const key = candidateKey(c);
        distillBusyKey = key;
        distillErrorByKey = { ...distillErrorByKey, [key]: '' };
        try {
            await distillSkill(project, c, gates, distillModel, 0);
            await load();
        } catch (e: any) {
            const msg = e?.message ?? String(e);
            distillErrorByKey = {
                ...distillErrorByKey,
                [key]: /below distillation threshold/i.test(msg)
                    ? get(t)('skillReview.belowThreshold')
                    : get(t)('skillReview.distillFailed', { message: msg }),
            };
        } finally {
            distillBusyKey = null;
        }
    }

    function toggleExpand(sk: Skill) {
        if (expandedId === sk.ID) {
            expandedId = null;
            return;
        }
        expandedId = sk.ID;
        conflictId = null;
        errorById = { ...errorById, [sk.ID]: '' };
        if (editedMdById[sk.ID] === undefined) {
            editedMdById = { ...editedMdById, [sk.ID]: sk.MD };
        }
    }

    async function onApprove(sk: Skill, overwrite = false) {
        if (busyId !== null) return;
        busyId = sk.ID;
        errorById = { ...errorById, [sk.ID]: '' };
        try {
            await approveSkill(sk.ID, editedMdById[sk.ID] ?? sk.MD, overwrite);
            conflictId = null;
            expandedId = null;
            await load();
        } catch (e: any) {
            const msg = e?.message ?? String(e);
            if (!overwrite && /already exists/i.test(msg)) {
                conflictId = sk.ID;
            } else {
                errorById = { ...errorById, [sk.ID]: msg };
            }
        } finally {
            busyId = null;
        }
    }

    async function onArchive(sk: Skill) {
        if (busyId !== null) return;
        busyId = sk.ID;
        errorById = { ...errorById, [sk.ID]: '' };
        try {
            await archiveSkill(sk.ID);
            if (expandedId === sk.ID) expandedId = null;
            await load();
        } catch (e: any) {
            errorById = { ...errorById, [sk.ID]: e?.message ?? String(e) };
        } finally {
            busyId = null;
        }
    }

    function statusLabel(status: string, tr: (key: string, params?: Record<string, string | number>) => string): string {
        switch (status) {
            case 'draft':
                return tr('skillReview.statusDraft');
            case 'approved':
                return tr('skillReview.statusApproved');
            case 'archived':
                return tr('skillReview.statusArchived');
            default:
                return status;
        }
    }

    function statusClass(status: string): string {
        switch (status) {
            case 'approved':
                return 'text-status-working';
            default:
                return 'text-status-waiting';
        }
    }

    // Archiving "removes it from the list" (LEARN-TASKS.md LN-10) — an
    // archived row is filtered out here rather than never fetched, so the
    // list stays a live view of ListSkills without a second query shape.
    $: activeSkills = skills.filter((s) => s.Status !== 'archived');
</script>

{#if !project}
    <div class="text-text-muted text-sm italic py-10 text-center">{$t('skillReview.noProjectConfigured')}</div>
{:else if loading}
    <div class="text-text-muted text-sm italic py-10 text-center">{$t('skillReview.loadingSkills')}</div>
{:else if error}
    <div class="text-status-error text-sm py-10 text-center">{error}</div>
{:else}
    {#if qualityError}
        <div class="px-3 py-2 text-status-error text-xs">{qualityError}</div>
    {:else if quality.length > 0}
        <div class="border-b border-bg-border">
            <div class="px-3 pt-2 pb-1 text-xs font-medium text-text-muted">
                {$t('skillReview.effectHeading')}
            </div>
            <table class="w-full text-xs border-collapse mb-2">
                <thead class="text-text-muted">
                    <tr>
                        <th class="text-left px-3 py-1 font-medium">{$t('skillReview.colSkill')}</th>
                        <th class="text-right px-3 py-1 font-medium">{$t('skillReview.colBefore')}</th>
                        <th class="text-right px-3 py-1 font-medium">{$t('skillReview.colAfter')}</th>
                        <th class="text-left px-3 py-1 font-medium">{$t('skillReview.colVerdict')}</th>
                    </tr>
                </thead>
                <tbody>
                    {#each quality as q (q.skill_id)}
                        <tr class="border-t border-bg-border">
                            <td class="px-3 py-1 text-text font-mono">{q.skill_name}</td>
                            <td class="px-3 py-1 text-right text-text-muted font-mono">
                                {q.before.runs} / {formatTokens(q.before.median_input_tokens)} / {q.before.median_num_turns}
                                ({formatPercent(q.before.completed_rate)})
                            </td>
                            <td class="px-3 py-1 text-right text-text-muted font-mono">
                                {q.after.runs} / {formatTokens(q.after.median_input_tokens)} / {q.after.median_num_turns}
                                ({formatPercent(q.after.completed_rate)})
                            </td>
                            <td class="px-3 py-1">
                                {#if q.insufficient_data}
                                    <span class="text-text-muted italic">{$t('skillReview.notEnoughData')}</span>
                                {:else if q.stale}
                                    <span class="text-status-waiting">
                                        {q.stale_reason === 'unused' ? $t('skillReview.suggestArchivingUnused') : $t('skillReview.suggestArchivingNoImprovement')}
                                    </span>
                                {:else}
                                    <span class="text-status-working">{$t('skillReview.verdictOk')}</span>
                                {/if}
                            </td>
                        </tr>
                    {/each}
                </tbody>
            </table>
        </div>
    {/if}

    {#if candidatesError}
        <div class="px-3 py-2 text-status-error text-xs">{candidatesError}</div>
    {:else if candidates.length > 0}
        <div class="border-b border-bg-border">
            <div class="px-3 pt-2 pb-1 flex items-center justify-between">
                <div class="text-xs font-medium text-text-muted">
                    {$t('skillReview.candidatesHeading')}
                </div>
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
            <table class="w-full text-xs border-collapse mb-2">
                <thead class="text-text-muted">
                    <tr>
                        <th class="text-left px-3 py-1 font-medium">{$t('skillReview.colSequence')}</th>
                        <th class="text-right px-3 py-1 font-medium">{$t('skillReview.colRuns')}</th>
                        <th class="text-right px-3 py-1 font-medium">{$t('skillReview.colScore')}</th>
                        <th class="px-3 py-1"></th>
                    </tr>
                </thead>
                <tbody>
                    {#each candidates as c (candidateKey(c))}
                        {@const key = candidateKey(c)}
                        <tr class="border-t border-bg-border align-top">
                            <td class="px-3 py-1 text-text font-mono">
                                {c.Sig.join(' → ')}
                                {#if c.ContextLossSuspect}
                                    <span class="ml-1 text-status-waiting">({$t('skillReview.loopSuspect')})</span>
                                {/if}
                                {#if c.Imported}
                                    <span class="ml-1 text-text-muted italic">({$t('skillReview.imported')})</span>
                                {/if}
                            </td>
                            <td class="px-3 py-1 text-right text-text-muted font-mono whitespace-nowrap">
                                {c.DistinctRuns} ({formatPercent(c.RunShare)})
                            </td>
                            <td class="px-3 py-1 text-right text-text-muted font-mono">{c.Score.toFixed(1)}</td>
                            <td class="px-3 py-1 text-right">
                                <button
                                    type="button"
                                    disabled={distillBusyKey !== null}
                                    on:click={() => onDistill(c)}
                                    class="px-2 py-0.5 rounded bg-status-working/80 hover:bg-status-working text-white disabled:opacity-50 whitespace-nowrap">
                                    {distillBusyKey === key ? $t('skillReview.distilling') : $t('skillReview.distillButton')}
                                </button>
                                {#if distillErrorByKey[key]}
                                    <div class="mt-1 text-status-error">{distillErrorByKey[key]}</div>
                                {/if}
                            </td>
                        </tr>
                    {/each}
                </tbody>
            </table>
        </div>
    {/if}
{/if}
{#if project && !loading && !error && activeSkills.length === 0}
    <div class="text-text-muted text-sm italic py-10 text-center">
        {$t('skillReview.noSkillsYet')}
    </div>
{:else if project && !loading && !error}
    <table class="w-full text-sm border-collapse">
        <thead class="bg-bg-elevated sticky top-0 z-10 text-text-muted text-xs">
            <tr>
                <th class="w-6"></th>
                <th class="text-left px-3 py-2 font-medium">{$t('skillReview.colName')}</th>
                <th class="text-left px-3 py-2 font-medium">{$t('skillReview.colDescription')}</th>
                <th class="text-left px-3 py-2 font-medium">{$t('skillReview.colStatus')}</th>
                <th class="text-left px-3 py-2 font-medium">{$t('skillReview.colCreated')}</th>
            </tr>
        </thead>
        <tbody>
            {#each activeSkills as sk (sk.ID)}
                {@const expanded = expandedId === sk.ID}
                {@const draft = parseSkillDraft(sk.DraftJSON)}
                <tr
                    class="border-t border-bg-border cursor-pointer
                           {expanded ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/60'}"
                    on:click={() => toggleExpand(sk)}>
                    <td class="px-2 py-1.5 text-text-muted text-xs text-center select-none">
                        {expanded ? '▼' : '▶'}
                    </td>
                    <td class="px-3 py-1.5 text-text font-mono text-xs">{sk.Name}</td>
                    <td class="px-3 py-1.5 text-text-muted text-xs">
                        {draft?.description ?? ''}
                    </td>
                    <td class="px-3 py-1.5 text-xs font-medium {statusClass(sk.Status)}">
                        {statusLabel(sk.Status, $t)}
                    </td>
                    <td class="px-3 py-1.5 text-text-muted font-mono text-xs">
                        {formatTime(sk.CreatedAt)}
                    </td>
                </tr>
                {#if expanded}
                    <tr class="bg-bg">
                        <td colspan="5" class="px-0 py-0">
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
                                    {#if sk.Status === 'draft'}
                                        <div class="flex items-center gap-2">
                                            <button
                                                type="button"
                                                disabled={busyId === sk.ID}
                                                on:click|stopPropagation={() => onArchive(sk)}
                                                class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg disabled:opacity-50">
                                                {$t('skillReview.archiveButton')}
                                            </button>
                                            <button
                                                type="button"
                                                disabled={busyId === sk.ID}
                                                on:click|stopPropagation={() => onApprove(sk)}
                                                class="px-2 py-1 text-xs rounded bg-status-working/80 hover:bg-status-working text-white disabled:opacity-50">
                                                {$t('skillReview.acceptButton')}
                                            </button>
                                        </div>
                                    {:else if sk.Status === 'approved'}
                                        <button
                                            type="button"
                                            disabled={busyId === sk.ID}
                                            on:click|stopPropagation={() => onArchive(sk)}
                                            class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg disabled:opacity-50">
                                            {$t('skillReview.archiveButton')}
                                        </button>
                                    {/if}
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

                                {#if conflictId === sk.ID}
                                    <div class="mt-2 text-xs">
                                        <span class="text-status-waiting">
                                            {$t('skillReview.skillFileExists', { name: sk.Name })}
                                        </span>
                                        <button
                                            type="button"
                                            on:click|stopPropagation={() => onApprove(sk, true)}
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
