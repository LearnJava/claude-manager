<script lang="ts">
    // One node of the roadmap tree (phase, task or subtask). Recurses into its
    // children via <svelte:self>. Extracted from RoadmapTree so the recursion
    // has a component boundary and each node owns its own expand/collapse
    // state.
    import { GetRoadmapTaskDetail, GetRoadmapRowDetail } from '../../wailsjs/go/main/App';
    import type { RoadmapNode } from './roadmap';
    import { t } from '../lib/i18n';

    export let node: RoadmapNode;
    export let project: string;
    export let roadmapFile: string;
    export let hideDone = false;
    export let depth = 0;
    // Phases start collapsed — a curated roadmap can hold hundreds of tasks —
    // except the branch that contains the task this session is on.
    export let autoExpand = false;

    let open = autoExpand || (node.kind !== 'phase' && depth > 0 && node.current);
    let showDetail = false;
    let detail: string | null = null;
    let detailError = '';
    let loading = false;

    $: children = (node.children ?? []).filter(
        (c) => !hideDone || c.status !== 'done' || c.done < c.total,
    );
    $: hasChildren = children.length > 0;
    // Detail text exists for a leaf when the roadmap either linked a file or
    // carried a note; the row itself is the fallback.
    $: canExpandDetail = node.kind !== 'phase';
    // A status file can queue rows from several roadmaps at once, so the row's
    // own file wins over the view-level one.
    $: nodeFile = node.roadmap_file || roadmapFile;

    async function toggleDetail() {
        showDetail = !showDetail;
        if (!showDetail || detail !== null || loading) return;
        loading = true;
        detailError = '';
        try {
            detail = node.detail_path
                ? await GetRoadmapTaskDetail(project, node.detail_path)
                : await GetRoadmapRowDetail(project, nodeFile, node.line);
        } catch (e: any) {
            detailError = e?.message ?? String(e);
        } finally {
            loading = false;
        }
    }

    function icon(status: string): string {
        if (status === 'done') return '✓';
        if (status === 'active') return '▸';
        if (status === 'blocked') return '⊘';
        return '○';
    }

    function statusClass(status: string): string {
        if (status === 'done') return 'text-status-working opacity-60';
        if (status === 'active') return 'text-status-working';
        if (status === 'blocked') return 'text-status-error';
        return 'text-text-muted';
    }
</script>

<li>
    <div class="flex items-start gap-1 text-xs leading-snug">
        {#if hasChildren}
            <button
                type="button"
                on:click={() => (open = !open)}
                title={open ? $t('roadmapNode.collapse') : $t('roadmapNode.expand')}
                class="shrink-0 w-3 text-center text-text-muted hover:text-text">
                {open ? '▾' : '▸'}
            </button>
        {:else}
            <span class="shrink-0 w-3"></span>
        {/if}

        {#if node.kind === 'phase'}
            <button
                type="button"
                on:click={() => (open = !open)}
                title={node.summary}
                class="text-left break-words flex-1 font-semibold text-text">
                {node.name || node.id}
                <span class="ml-1 font-normal text-text-muted">{node.done}/{node.total}</span>
            </button>
        {:else}
            <span class="shrink-0 w-3 text-center {statusClass(node.status)}">{icon(node.status)}</span>

            {#if canExpandDetail}
                <button
                    type="button"
                    on:click={toggleDetail}
                    title={node.detail_path || $t('roadmapNode.showDescriptionTitle')}
                    class="shrink-0 w-3 text-center text-text-muted hover:text-text">
                    {showDetail ? '−' : '+'}
                </button>
            {/if}

            <button
                type="button"
                on:click={toggleDetail}
                class="text-left break-words flex-1
                       {node.status === 'done' ? 'text-text-muted line-through' : 'text-text'}
                       {node.current ? 'font-semibold' : ''}">
                {#if node.number > 0}{node.number}. {/if}{node.name || node.summary}
                {#if node.name && node.summary}<span class="text-text-muted"> — {node.summary}</span>{/if}
                {#if node.current}<span class="ml-1 text-[10px] text-status-working">← {$t('roadmapNode.nowMarker')}</span>
                {:else if node.in_queue}<span class="ml-1 text-[10px] text-text-muted">{$t('roadmapNode.queued')}</span>{/if}
                {#if hasChildren}<span class="ml-1 text-[10px] text-text-muted">{node.done}/{node.total}</span>{/if}
            </button>
        {/if}
    </div>

    {#if showDetail && node.kind !== 'phase'}
        <div class="ml-7 mt-1 mb-2 pl-2 border-l border-bg-border">
            {#if node.depends_on && node.depends_on !== '-'}
                <div class="text-[10px] text-text-muted">{$t('roadmapNode.dependsOn', { value: node.depends_on })}</div>
            {/if}
            {#if node.bugs}
                <div class="text-[10px] text-text-muted">{$t('roadmapNode.bugs', { value: node.bugs })}</div>
            {/if}
            {#if node.status_raw}
                <div class="text-[10px] text-text-muted">{$t('roadmapNode.status', { status: node.status_raw })}{node.size ? $t('roadmapNode.sizeSuffix', { size: node.size }) : ''}</div>
            {/if}
            {#if detailError}
                <div class="text-[11px] text-status-error break-words">{detailError}</div>
            {:else if loading}
                <div class="text-[11px] text-text-muted">{$t('common.loading')}</div>
            {:else if detail}
                <pre class="mt-1 text-[11px] text-text-muted whitespace-pre-wrap break-words font-sans">{detail}</pre>
            {/if}
        </div>
    {/if}

    {#if open && hasChildren}
        <ul class="ml-3 border-l border-bg-border pl-1 space-y-0.5">
            {#each children as child (child.id + ':' + child.line)}
                <svelte:self
                    node={child}
                    {project}
                    roadmapFile={nodeFile}
                    {hideDone}
                    depth={depth + 1}
                    autoExpand={false} />
            {/each}
        </ul>
    {/if}
</li>
