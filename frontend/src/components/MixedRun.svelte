<script lang="ts">
    import { createEventDispatcher, onMount } from 'svelte';
    import { GetConfig } from '../../wailsjs/go/main/App';
    import {
        mixedTasks,
        mixedQuality,
        workerActivity,
        dispatching,
        initWorkers,
        loadProject,
        dispatch as dispatchTask,
        cancel as cancelTask,
        clearActivity,
        type MixedTask,
        type RoundRecord,
        type GateResult,
    } from '../stores/workers';
    import { formatPercent } from '../lib/formatters';

    const dispatch = createEventDispatcher();

    interface WorkerCfg {
        Name: string;
        Role: string;
        Model: string;
    }
    interface ProjectCfg {
        Name: string;
        MixedProgramming?: boolean;
        Gates?: string[];
    }

    let mixedProjects: ProjectCfg[] = [];
    let workers: WorkerCfg[] = [];

    let selectedProject = '';
    let selectedWorker = '';
    let briefID = '';
    let briefTask = '';

    let loading = true;
    let error = '';
    let info = '';

    // Which round timelines are expanded (task ID -> round number -> open).
    let expandedGates: Record<string, boolean> = {};

    function close() {
        dispatch('close');
    }
    function handleKey(e: KeyboardEvent) {
        if (e.key === 'Escape') close();
    }

    async function loadConfig() {
        loading = true;
        error = '';
        try {
            const cfg = (await GetConfig()) as any;
            workers = ((cfg?.Workers ?? []) as WorkerCfg[]).map((w) => ({
                Name: w.Name,
                Role: w.Role,
                Model: w.Model,
            }));
            mixedProjects = ((cfg?.Projects ?? []) as ProjectCfg[]).filter((p) => p.MixedProgramming);

            if (mixedProjects.length > 0) {
                selectedProject = mixedProjects[0].Name;
            }
            if (workers.length > 0) {
                selectedWorker = workers[0].Name;
            }
            if (selectedProject) {
                await loadProject(selectedProject);
            }
        } catch (e: any) {
            error = `Load failed: ${e?.message ?? String(e)}`;
        } finally {
            loading = false;
        }
    }

    onMount(() => {
        initWorkers();
        loadConfig();
    });

    async function onProjectChange() {
        clearActivity();
        if (selectedProject) await loadProject(selectedProject);
    }

    async function runDispatch() {
        error = '';
        info = '';
        if (!selectedProject || !selectedWorker) {
            error = 'Select a project and a worker.';
            return;
        }
        const id = briefID.trim() || `brief-${Date.now()}`;
        if (!briefTask.trim()) {
            error = 'Enter a task description for the brief.';
            return;
        }
        try {
            const result = await dispatchTask(selectedProject, id, briefTask.trim(), selectedWorker);
            info = `Task ${result.ID} finished: ${result.Status}`;
        } catch (e: any) {
            error = `Dispatch failed: ${e?.message ?? String(e)}`;
        }
    }

    async function onCancel(taskID: string) {
        try {
            await cancelTask(taskID);
        } catch (e: any) {
            error = `Cancel failed: ${e?.message ?? String(e)}`;
        }
    }

    function toggleGate(taskID: string, round: number) {
        const key = `${taskID}#${round}`;
        expandedGates = { ...expandedGates, [key]: !expandedGates[key] };
    }

    function gateOutput(gates: GateResult): string {
        if (!gates?.Commands) return '';
        return gates.Commands
            .map((c) => `$ ${c.Command} (exit ${c.ExitCode})\n${c.Output}`)
            .join('\n\n');
    }

    function statusColor(status: string): string {
        if (status === 'done') return 'text-status-working';
        if (status === 'needs_human') return 'text-status-error';
        return 'text-status-waiting';
    }

    function roundVerdict(r: RoundRecord): { label: string; cls: string } {
        if (r.ParseError) return { label: 'parse error', cls: 'text-status-error' };
        if ((r.Rejected?.length ?? 0) > 0) return { label: 'patches rejected', cls: 'text-status-error' };
        if (r.Passed) return { label: 'passed', cls: 'text-status-working' };
        return { label: 'gates failed', cls: 'text-status-ratelimit' };
    }

    // Sort tasks so running ones surface first, then by ID for stability.
    $: sortedTasks = [...$mixedTasks].sort((a, b) => {
        const rank = (t: MixedTask) => (t.Status === 'running' ? 0 : t.Status === 'needs_human' ? 1 : 2);
        const d = rank(a) - rank(b);
        return d !== 0 ? d : a.ID.localeCompare(b.ID);
    });
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
        class="bg-bg-panel border border-bg-border rounded-md shadow-xl w-[920px] max-w-[95vw] h-[680px] max-h-[92vh] flex flex-col"
        role="document"
        on:click|stopPropagation
        on:keydown|stopPropagation>
        <!-- Header -->
        <div class="px-4 py-3 border-b border-bg-border flex items-center justify-between shrink-0">
            <h2 class="text-text font-semibold text-base">Mixed programming</h2>
            <button
                class="text-text-muted hover:text-text text-sm px-2 py-0.5"
                on:click={close}
                type="button">✕</button>
        </div>

        <!-- Body -->
        <div class="flex-1 min-h-0 overflow-y-auto px-4 py-4 space-y-5">
            {#if loading}
                <div class="text-text-muted text-sm italic py-10 text-center">Loading…</div>
            {:else if mixedProjects.length === 0}
                <div class="text-text-muted text-sm py-10 text-center">
                    No project has mixed programming enabled. Turn on
                    <b>mixed_programming</b> for a project in Settings → Workers first.
                </div>
            {:else}
                <!-- ───────── Dispatch form ───────── -->
                <section class="bg-bg-elevated border border-bg-border rounded p-3 space-y-3">
                    <h3 class="text-text font-semibold text-sm">Dispatch a task</h3>
                    <div class="grid grid-cols-2 gap-3">
                        <label class="flex flex-col text-xs text-text-muted gap-1">
                            Project
                            <select
                                data-testid="mixed-project"
                                bind:value={selectedProject}
                                on:change={onProjectChange}
                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                {#each mixedProjects as p}
                                    <option value={p.Name}>{p.Name}</option>
                                {/each}
                            </select>
                        </label>
                        <label class="flex flex-col text-xs text-text-muted gap-1">
                            Worker
                            <select
                                data-testid="mixed-worker"
                                bind:value={selectedWorker}
                                class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text">
                                {#if workers.length === 0}
                                    <option value="">(no workers configured)</option>
                                {/if}
                                {#each workers as w}
                                    <option value={w.Name}>{w.Name} — {w.Role} ({w.Model})</option>
                                {/each}
                            </select>
                        </label>
                    </div>
                    <label class="flex flex-col text-xs text-text-muted gap-1">
                        Brief ID (optional)
                        <input
                            type="text"
                            bind:value={briefID}
                            placeholder="auto-generated if empty"
                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono" />
                    </label>
                    <label class="flex flex-col text-xs text-text-muted gap-1">
                        Brief / task
                        <textarea
                            data-testid="mixed-brief"
                            bind:value={briefTask}
                            rows="3"
                            placeholder="Self-contained task in English: verbatim code, accepted decisions, patch format…"
                            class="bg-bg border border-bg-border rounded px-2 py-1 text-sm text-text font-mono resize-y"
                        ></textarea>
                    </label>
                    <div class="flex items-center gap-2">
                        <button
                            type="button"
                            data-testid="mixed-dispatch"
                            on:click={runDispatch}
                            disabled={$dispatching}
                            class="px-3 py-1 text-sm rounded font-medium bg-blue-600 hover:bg-blue-500 text-white
                                   disabled:opacity-50 disabled:cursor-not-allowed">
                            {$dispatching ? 'Running…' : 'Dispatch'}
                        </button>
                        <p class="text-xs text-text-dim">
                            Code is sent to the worker's external endpoint.
                        </p>
                    </div>
                </section>

                <!-- ───────── Live activity ───────── -->
                {#if $workerActivity.length > 0}
                    <section>
                        <div class="flex items-center justify-between mb-1">
                            <h3 class="text-text font-semibold text-sm">Activity</h3>
                            <button
                                type="button"
                                on:click={clearActivity}
                                class="text-xs text-text-muted hover:text-text">clear</button>
                        </div>
                        <div
                            data-testid="mixed-activity"
                            class="bg-bg border border-bg-border rounded p-2 max-h-32 overflow-y-auto font-mono text-xs space-y-0.5">
                            {#each $workerActivity as a}
                                <div class="text-text-muted">
                                    <span class="text-text-dim">{a.task_id}</span> — {a.message}
                                </div>
                            {/each}
                        </div>
                    </section>
                {/if}

                <!-- ───────── Quality report ───────── -->
                <section>
                    <h3 class="text-text font-semibold text-sm mb-2">Model quality</h3>
                    {#if $mixedQuality.length === 0}
                        <div class="text-xs text-text-muted italic">No completed tasks yet.</div>
                    {:else}
                        <table class="w-full text-xs" data-testid="mixed-quality">
                            <thead class="text-text-muted">
                                <tr class="text-left border-b border-bg-border">
                                    <th class="py-1 pr-2">Worker</th>
                                    <th class="py-1 px-2">Done / total</th>
                                    <th class="py-1 px-2">Needs human</th>
                                    <th class="py-1 px-2" title="Mean rounds until gates pass">Avg rounds→green</th>
                                    <th class="py-1 px-2" title="applied / (applied + rejected)">Clean patches</th>
                                    <th class="py-1 px-2">Parse errs</th>
                                    <th class="py-1 pl-2">Gate fails</th>
                                </tr>
                            </thead>
                            <tbody class="text-text">
                                {#each $mixedQuality as q}
                                    <tr class="border-b border-bg-border/50">
                                        <td class="py-1 pr-2 font-mono">{q.worker}</td>
                                        <td class="py-1 px-2">{q.tasks_done} / {q.tasks_total}</td>
                                        <td class="py-1 px-2">{q.tasks_needs_human}</td>
                                        <td class="py-1 px-2">{q.tasks_done > 0 ? q.avg_rounds_to_green.toFixed(1) : '—'}</td>
                                        <td class="py-1 px-2">{formatPercent(q.clean_patch_rate)}</td>
                                        <td class="py-1 px-2">{q.parse_errors}</td>
                                        <td class="py-1 pl-2">{q.gate_failures}</td>
                                    </tr>
                                {/each}
                            </tbody>
                        </table>
                    {/if}
                </section>

                <!-- ───────── Task timelines ───────── -->
                <section>
                    <h3 class="text-text font-semibold text-sm mb-2">Tasks</h3>
                    {#if sortedTasks.length === 0}
                        <div class="text-xs text-text-muted italic">No tasks for this project yet.</div>
                    {:else}
                        <ul class="space-y-3" data-testid="mixed-tasks">
                            {#each sortedTasks as t (t.ID)}
                                <li class="bg-bg-elevated border border-bg-border rounded p-3">
                                    <div class="flex items-center justify-between mb-2">
                                        <div class="text-sm">
                                            <span class="font-mono text-text">{t.BriefID}</span>
                                            <span class="text-text-muted"> · {t.WorkerName}</span>
                                            <span class={`ml-2 font-medium ${statusColor(t.Status)}`}>{t.Status}</span>
                                        </div>
                                        {#if t.Status === 'running'}
                                            <button
                                                type="button"
                                                on:click={() => onCancel(t.ID)}
                                                class="px-2 py-0.5 text-xs rounded bg-status-error/80 hover:bg-status-error text-white">
                                                Cancel
                                            </button>
                                        {/if}
                                    </div>
                                    {#if t.Branch}
                                        <div class="text-xs text-text-dim font-mono mb-2">branch: {t.Branch}</div>
                                    {/if}
                                    {#if t.Error}
                                        <div class="text-xs text-status-error mb-2">error: {t.Error}</div>
                                    {/if}

                                    <ol class="space-y-1">
                                        {#each t.Rounds ?? [] as r (r.Number)}
                                            {@const v = roundVerdict(r)}
                                            <li class="text-xs">
                                                <div class="flex items-center gap-2">
                                                    <span class="text-text-muted">Round {r.Number}</span>
                                                    <span class="text-text-dim">
                                                        {(r.Applied?.length ?? 0)} applied,
                                                        {(r.Rejected?.length ?? 0)} rejected
                                                    </span>
                                                    <span class={`font-medium ${v.cls}`}>{v.label}</span>
                                                    {#if r.Gates?.Commands}
                                                        <button
                                                            type="button"
                                                            on:click={() => toggleGate(t.ID, r.Number)}
                                                            class="text-text-muted hover:text-text underline">
                                                            {expandedGates[`${t.ID}#${r.Number}`] ? 'hide' : 'gates'}
                                                        </button>
                                                    {/if}
                                                </div>
                                                {#if r.ParseError}
                                                    <div class="text-status-error font-mono mt-0.5">{r.ParseError}</div>
                                                {/if}
                                                {#if expandedGates[`${t.ID}#${r.Number}`]}
                                                    <pre class="mt-1 bg-bg border border-bg-border rounded p-2 overflow-x-auto text-text-muted whitespace-pre-wrap">{gateOutput(r.Gates)}</pre>
                                                {/if}
                                            </li>
                                        {/each}
                                    </ol>
                                </li>
                            {/each}
                        </ul>
                    {/if}
                </section>
            {/if}
        </div>

        <!-- Footer -->
        <div class="px-4 py-3 border-t border-bg-border flex items-center justify-between shrink-0">
            <div class="text-xs">
                {#if error}
                    <span class="text-status-error">{error}</span>
                {:else if info}
                    <span class="text-status-working">{info}</span>
                {:else}
                    <span class="text-text-muted">Worktrees are left in place for review after each task.</span>
                {/if}
            </div>
            <button
                type="button"
                on:click={close}
                class="px-3 py-1 text-sm rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg">
                Close
            </button>
        </div>
    </div>
</div>
