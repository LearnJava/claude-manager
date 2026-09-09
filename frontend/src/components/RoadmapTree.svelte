<script lang="ts">
    // Roadmap tab of the TaskPanel: the project's ROADMAP.md as a tree —
    // phases at the root, tasks and subtasks below, each expandable to its
    // full description.
    //
    // Two status models exist (see internal/analysis/roadmapview.go): a
    // generated roadmap infers "done" from the session's pointer file, while a
    // curated one carries its own status column and the pointers only mark
    // what this session took. The header says which is in use, because "3/570
    // done" means very different things under the two.
    import { GetSessionRoadmap } from '../../wailsjs/go/main/App';
    import RoadmapNodeView from './RoadmapNode.svelte';
    import { containsCurrent, type RoadmapView } from './roadmap';
    import type { SessionState } from '../stores/sessions';
    import { t } from '../lib/i18n';

    export let session: SessionState;

    let view: RoadmapView | null = null;
    let loading = false;
    let error = '';
    let hideDone = false;
    let showContext = false;

    // Reload on session change, and whenever the backend reports a new task
    // description or a finished task — both mean a pointer line was consumed,
    // which is exactly what changes the tree.
    let lastKey = '';
    $: {
        const key = `${session.project}/${session.name}/${session.task_source_description ?? ''}/${session.tasks_done ?? 0}`;
        if (key !== lastKey) {
            lastKey = key;
            void load();
        }
    }

    async function load() {
        loading = true;
        error = '';
        try {
            view = ((await GetSessionRoadmap(session.project, session.name)) as RoadmapView | null) ?? null;
        } catch (e: any) {
            error = e?.message ?? String(e);
            view = null;
        } finally {
            loading = false;
        }
    }

    $: roots = (view?.nodes ?? []).filter((n) => !hideDone || n.done < n.total);
    $: percent = view && view.total > 0 ? Math.round((view.done / view.total) * 100) : 0;
</script>

<div class="space-y-3">
    {#if loading && !view}
        <div class="text-xs text-text-muted">{$t('roadmapTree.loadingRoadmap')}</div>
    {:else if error}
        <div class="text-xs text-status-error break-words">{error}</div>
    {:else if !view}
        <div class="text-xs text-text-muted">
            {$t('roadmapTree.noRoadmapBefore')}
            <code class="font-mono">ROADMAP.md</code> {$t('roadmapTree.noRoadmapAfter')}
        </div>
    {:else}
        <div>
            <div class="flex items-center justify-between gap-2">
                <span class="text-xs font-semibold text-text break-words">
                    📋 {view.title || view.roadmap_file}
                </span>
                <button
                    type="button"
                    on:click={load}
                    title={$t('roadmapTree.reloadRoadmapTitle')}
                    class="shrink-0 text-xs text-text-muted hover:text-text">🔄</button>
            </div>
            <div class="mt-1 flex items-center justify-between gap-2">
                <span class="text-[10px] text-text-muted">
                    {$t('roadmapTree.progressSummary', { done: view.done, total: view.total, percent })}
                    {#if view.status_model !== 'pointer'}
                        <span title={$t('roadmapTree.statusModelTitle')}>
                            · {view.status_model}
                        </span>
                    {/if}
                </span>
                <button
                    type="button"
                    on:click={() => (hideDone = !hideDone)}
                    class="shrink-0 text-[10px] text-text-muted hover:text-text">
                    {hideDone ? $t('roadmapTree.showDone') : $t('roadmapTree.hideDone')}
                </button>
            </div>
            <div class="mt-1 h-1.5 rounded bg-bg-elevated overflow-hidden">
                <div
                    class="h-full rounded bg-status-working transition-all duration-500"
                    style="width: {percent}%"></div>
            </div>
        </div>

        {#if view.context}
            <div>
                <button
                    type="button"
                    class="text-[10px] uppercase tracking-wide text-text-muted hover:text-text"
                    on:click={() => (showContext = !showContext)}>
                    {showContext ? '− ' : '+ '}{$t('roadmapTree.projectContext')}
                </button>
                {#if showContext}
                    <div class="mt-1 text-[11px] text-text-muted whitespace-pre-wrap break-words">
                        {view.context}
                    </div>
                {/if}
            </div>
        {/if}

        {#if roots.length === 0}
            <div class="text-xs text-text-muted">{$t('roadmapTree.everythingDone')}</div>
        {:else}
            <ul class="space-y-0.5">
                {#each roots as node (node.id + ':' + node.line)}
                    <RoadmapNodeView
                        {node}
                        project={session.project}
                        roadmapFile={view.roadmap_file}
                        {hideDone}
                        depth={0}
                        autoExpand={containsCurrent(node) ||
                            (view.nodes.length <= 3 && node.total <= 50)} />
                {/each}
            </ul>
        {/if}
    {/if}
</div>
