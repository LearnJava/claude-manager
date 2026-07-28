<script lang="ts">
    // Roadmap tab of the TaskPanel: the project's ROADMAP.md rendered as a
    // tree — roadmap title at the root, one leaf per task, each expandable to
    // show the full text of its tasks/NN-*.md detail file.
    //
    // Status comes from the backend and is derived, not stored: a task is done
    // when its pointer line is gone from STATUS-P1.md (see ReadRoadmap).
    import { onDestroy } from 'svelte';
    import { GetSessionRoadmap, GetRoadmapTaskDetail } from '../../wailsjs/go/main/App';
    import type { SessionState } from '../stores/sessions';

    export let session: SessionState;

    type RoadmapTask = {
        number: number;
        name: string;
        summary: string;
        detail_path: string;
        depends_on: string;
        line: number;
        status: 'done' | 'current' | 'pending';
    };
    type RoadmapView = {
        title: string;
        roadmap_file: string;
        status_file: string;
        context: string;
        tasks: RoadmapTask[];
        done: number;
        total: number;
    };

    let view: RoadmapView | null = null;
    let loading = false;
    let error = '';
    let expanded = new Set<number>();
    let details: Record<number, string> = {};
    let detailErrors: Record<number, string> = {};
    let showContext = false;

    // Reload when the session changes, and whenever the backend reports a new
    // task-source description — that is the signal a pointer line was consumed
    // (task finished), which is exactly what changes the tree.
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
            const res = (await GetSessionRoadmap(session.project, session.name)) as RoadmapView | null;
            view = res ?? null;
            // Drop cached bodies: line numbers and files may have changed.
            details = {};
            detailErrors = {};
        } catch (e: any) {
            error = e?.message ?? String(e);
            view = null;
        } finally {
            loading = false;
        }
    }

    async function toggle(task: RoadmapTask) {
        if (expanded.has(task.number)) {
            expanded.delete(task.number);
            expanded = expanded;
            return;
        }
        expanded.add(task.number);
        expanded = expanded;

        if (details[task.number] !== undefined || !task.detail_path) return;
        try {
            details[task.number] = await GetRoadmapTaskDetail(session.project, task.detail_path);
            details = details;
        } catch (e: any) {
            detailErrors[task.number] = e?.message ?? String(e);
            detailErrors = detailErrors;
        }
    }

    function icon(status: string): string {
        if (status === 'done') return '✓';
        if (status === 'current') return '▸';
        return '○';
    }

    $: percent = view && view.total > 0 ? Math.round((view.done / view.total) * 100) : 0;

    onDestroy(() => {
        expanded = new Set();
    });
</script>

<div class="space-y-3">
    {#if loading && !view}
        <div class="text-xs text-text-muted">Loading roadmap…</div>
    {:else if error}
        <div class="text-xs text-status-error break-words">{error}</div>
    {:else if !view}
        <div class="text-xs text-text-muted">
            No roadmap for this session. A roadmap appears here once the project
            has a <code class="font-mono">ROADMAP.md</code> and the session points at a
            task source (Settings → Projects → Generate Roadmap with AI).
        </div>
    {:else}
        <!-- Root: the roadmap itself -->
        <div>
            <div class="flex items-center justify-between gap-2">
                <span class="text-xs font-semibold text-text break-words">
                    📋 {view.title || view.roadmap_file}
                </span>
                <button
                    type="button"
                    on:click={load}
                    title="Reload roadmap"
                    class="shrink-0 text-xs text-text-muted hover:text-text">🔄</button>
            </div>
            <div class="mt-1 text-[10px] text-text-muted">
                {view.done}/{view.total} done · {percent}%
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
                    {showContext ? '− ' : '+ '}Project context
                </button>
                {#if showContext}
                    <div class="mt-1 text-[11px] text-text-muted whitespace-pre-wrap break-words">
                        {view.context}
                    </div>
                {/if}
            </div>
        {/if}

        <!-- Leaves: one per task -->
        <ul class="space-y-0.5 border-l border-bg-border pl-2">
            {#each view.tasks as task (task.number)}
                <li>
                    <div class="flex items-start gap-1 text-xs leading-snug">
                        <span
                            class:text-status-working={task.status === 'done' || task.status === 'current'}
                            class:opacity-60={task.status === 'done'}
                            class:text-text-muted={task.status === 'pending'}
                            class="shrink-0 w-3 text-center">{icon(task.status)}</span>

                        <button
                            type="button"
                            on:click={() => toggle(task)}
                            title={task.detail_path || 'No detail file for this task'}
                            class="shrink-0 w-3 text-center text-text-muted hover:text-text">
                            {expanded.has(task.number) ? '−' : '+'}
                        </button>

                        <button
                            type="button"
                            on:click={() => toggle(task)}
                            class="text-left break-words flex-1
                                   {task.status === 'done' ? 'text-text-muted line-through' : 'text-text'}
                                   {task.status === 'current' ? 'font-semibold' : ''}">
                            <span>{task.number}. {task.name || task.summary}</span>
                            {#if task.name && task.summary}
                                <span class="text-text-muted"> — {task.summary}</span>
                            {/if}
                        </button>
                    </div>

                    {#if expanded.has(task.number)}
                        <div class="ml-7 mt-1 mb-2 pl-2 border-l border-bg-border">
                            {#if task.depends_on && task.depends_on !== '-'}
                                <div class="text-[10px] text-text-muted mb-1">
                                    Depends on: {task.depends_on}
                                </div>
                            {/if}
                            {#if detailErrors[task.number]}
                                <div class="text-[11px] text-status-error break-words">
                                    {detailErrors[task.number]}
                                </div>
                            {:else if !task.detail_path}
                                <div class="text-[11px] text-text-muted break-words">
                                    {task.summary || 'No description in the roadmap row.'}
                                    <div class="mt-1 text-text-muted/70">
                                        This roadmap predates per-task files, so the row above is
                                        the whole task.
                                    </div>
                                </div>
                            {:else if details[task.number] === undefined}
                                <div class="text-[11px] text-text-muted">Loading…</div>
                            {:else}
                                <pre class="text-[11px] text-text-muted whitespace-pre-wrap break-words font-sans">{details[
                                    task.number
                                ]}</pre>
                            {/if}
                        </div>
                    {/if}
                </li>
            {/each}
        </ul>
    {/if}
</div>
