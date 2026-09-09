<script lang="ts">
    // The "Skills" tab body (LEARN-TASKS.md LN-10): review/edit/accept/archive
    // the drafts LN-09's distiller produced. Extracted out of ExperiencePanel
    // (mounted as <SkillReview {project} />) so the review/edit surface — which
    // is meaningfully bigger than the Actions/Permissions/Timing tabs' plain
    // tables — doesn't balloon that file.
    import { formatPercent, formatTime, formatTokens } from '../lib/formatters';
    import { renderMarkdown } from '../lib/markdown';
    import {
        approveSkill,
        archiveSkill,
        fetchSkillQuality,
        fetchSkills,
        parseSkillDraft,
        type Skill,
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
            loading = false;
            return;
        }
        loading = true;
        error = '';
        try {
            skills = await fetchSkills(project);
        } catch (e: any) {
            error = `Failed to load skills: ${e?.message ?? String(e)}`;
            skills = [];
        } finally {
            loading = false;
        }
        qualityError = '';
        try {
            quality = await fetchSkillQuality(project);
        } catch (e: any) {
            qualityError = `Failed to load skill quality: ${e?.message ?? String(e)}`;
            quality = [];
        }
    }

    $: if (project) load();

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

    function statusLabel(status: string): string {
        switch (status) {
            case 'draft':
                return 'Draft';
            case 'approved':
                return 'Approved';
            case 'archived':
                return 'Archived';
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
    <div class="text-text-muted text-sm italic py-10 text-center">No project configured.</div>
{:else if loading}
    <div class="text-text-muted text-sm italic py-10 text-center">Loading skills…</div>
{:else if error}
    <div class="text-status-error text-sm py-10 text-center">{error}</div>
{:else}
    {#if qualityError}
        <div class="px-3 py-2 text-status-error text-xs">{qualityError}</div>
    {:else if quality.length > 0}
        <div class="border-b border-bg-border">
            <div class="px-3 pt-2 pb-1 text-xs font-medium text-text-muted">
                Effect (before vs. after approval — LEARN-TASKS.md LN-11)
            </div>
            <table class="w-full text-xs border-collapse mb-2">
                <thead class="text-text-muted">
                    <tr>
                        <th class="text-left px-3 py-1 font-medium">Skill</th>
                        <th class="text-right px-3 py-1 font-medium">Before (runs / tokens / turns)</th>
                        <th class="text-right px-3 py-1 font-medium">After (runs / tokens / turns)</th>
                        <th class="text-left px-3 py-1 font-medium">Verdict</th>
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
                                    <span class="text-text-muted italic">Not enough data</span>
                                {:else if q.stale}
                                    <span class="text-status-waiting">
                                        Suggest archiving
                                        {q.stale_reason === 'unused' ? '(unused)' : '(no token improvement)'}
                                    </span>
                                {:else}
                                    <span class="text-status-working">OK</span>
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
        No distilled skills for this project yet — skills come from LEARN-TASKS.md LN-09
        (candidate mining + distillation), not from this tab.
    </div>
{:else if project && !loading && !error}
    <table class="w-full text-sm border-collapse">
        <thead class="bg-bg-elevated sticky top-0 z-10 text-text-muted text-xs">
            <tr>
                <th class="w-6"></th>
                <th class="text-left px-3 py-2 font-medium">Name</th>
                <th class="text-left px-3 py-2 font-medium">Description</th>
                <th class="text-left px-3 py-2 font-medium">Status</th>
                <th class="text-left px-3 py-2 font-medium">Created</th>
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
                        {statusLabel(sk.Status)}
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
                                            Edit
                                        </button>
                                        <button
                                            type="button"
                                            on:click|stopPropagation={() =>
                                                (previewById = { ...previewById, [sk.ID]: true })}
                                            class="px-2 py-1 rounded border
                                                {previewById[sk.ID]
                                                    ? 'bg-bg-elevated border-blue-500 text-text'
                                                    : 'border-bg-border text-text-muted hover:text-text hover:bg-bg-elevated/60'}">
                                            Preview
                                        </button>
                                    </div>
                                    {#if sk.Status === 'draft'}
                                        <div class="flex items-center gap-2">
                                            <button
                                                type="button"
                                                disabled={busyId === sk.ID}
                                                on:click|stopPropagation={() => onArchive(sk)}
                                                class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg disabled:opacity-50">
                                                Archive
                                            </button>
                                            <button
                                                type="button"
                                                disabled={busyId === sk.ID}
                                                on:click|stopPropagation={() => onApprove(sk)}
                                                class="px-2 py-1 text-xs rounded bg-status-working/80 hover:bg-status-working text-white disabled:opacity-50">
                                                Accept
                                            </button>
                                        </div>
                                    {:else if sk.Status === 'approved'}
                                        <button
                                            type="button"
                                            disabled={busyId === sk.ID}
                                            on:click|stopPropagation={() => onArchive(sk)}
                                            class="px-2 py-1 text-xs rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg disabled:opacity-50">
                                            Archive
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
                                            {sk.Name}/SKILL.md already exists in this project.
                                        </span>
                                        <button
                                            type="button"
                                            on:click|stopPropagation={() => onApprove(sk, true)}
                                            disabled={busyId === sk.ID}
                                            class="ml-2 px-2 py-0.5 rounded bg-status-error/80 hover:bg-status-error text-white disabled:opacity-50">
                                            Yes, overwrite
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
