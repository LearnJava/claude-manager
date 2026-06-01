<script lang="ts">
    import { createEventDispatcher } from 'svelte';

    export let project: string;
    export let sessionName: string;

    const dispatch = createEventDispatcher<{
        confirm: { model: string; effort: string };
        cancel: void;
    }>();

    const MODELS  = ['haiku', 'sonnet', 'opus'];
    const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'];

    // Recommendation loaded from backend (null = not yet loaded)
    type Rec = { model: string; effort: string; complexity: string; reason: string } | null;

    let rec: Rec = null;
    let loading = true;
    let error = '';

    let chosenModel  = 'sonnet';
    let chosenEffort = 'medium';
    let overriding   = false;   // true = user opened the override dropdowns

    import { GetModelRecommendation } from '../../wailsjs/go/main/App';

    async function load() {
        loading = true;
        error = '';
        try {
            rec = await GetModelRecommendation(project, sessionName) as Rec;
            if (rec) {
                chosenModel  = rec.model;
                chosenEffort = rec.effort;
            }
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            loading = false;
        }
    }

    load();

    function confirm() {
        dispatch('confirm', { model: chosenModel, effort: chosenEffort });
    }

    function cancel() {
        dispatch('cancel');
    }

    const complexityLabel: Record<string, string> = {
        trivial:       'Trivial',
        standard:      'Standard',
        complex:       'Complex',
        architectural: 'Architectural',
    };
    const complexityColor: Record<string, string> = {
        trivial:       'text-status-working',
        standard:      'text-blue-400',
        complex:       'text-status-ratelimit',
        architectural: 'text-status-error',
    };
</script>

<!-- Backdrop -->
<div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    on:click={cancel}
    on:keydown={(e) => e.key === 'Escape' && cancel()}
    role="dialog"
    aria-modal="true"
    tabindex="-1">

    <div
        class="bg-bg-panel border border-bg-border rounded-lg shadow-2xl w-[420px] p-5 flex flex-col gap-4"
        role="document"
        on:click|stopPropagation
        on:keydown|stopPropagation>

        <!-- Header -->
        <div class="flex items-center justify-between">
            <div>
                <span class="text-text font-semibold">Start session</span>
                <span class="text-text-muted ml-2" style="font-size:13px">{project}/{sessionName}</span>
            </div>
            <button class="text-text-muted hover:text-text" on:click={cancel} type="button">✕</button>
        </div>

        <!-- Body -->
        {#if loading}
            <div class="text-text-muted text-sm py-4 text-center">
                Analyzing task complexity…
            </div>
        {:else if error}
            <div class="text-status-error text-sm py-2">{error}</div>
        {:else if rec}
            <!-- Recommendation -->
            <div class="bg-bg-elevated border border-bg-border rounded p-3 space-y-1">
                <div class="flex items-center gap-2" style="font-size:13px">
                    <span class="text-text-muted">Complexity:</span>
                    <span class="font-semibold {complexityColor[rec.complexity] ?? 'text-text'}">
                        {complexityLabel[rec.complexity] ?? rec.complexity}
                    </span>
                </div>
                <div style="font-size:13px" class="text-text-muted">{rec.reason}</div>
                <div class="flex items-center gap-3 mt-1">
                    <span class="text-text-muted" style="font-size:13px">Recommended:</span>
                    <span class="font-mono font-semibold text-blue-400">{rec.model}</span>
                    <span class="text-text-muted" style="font-size:13px">effort:</span>
                    <span class="font-mono font-semibold text-blue-400">{rec.effort}</span>
                </div>
            </div>
        {:else}
            <div class="text-text-muted text-sm">No recommendation (no prompt configured).</div>
        {/if}

        <!-- Override toggle -->
        {#if !loading}
            <button
                type="button"
                class="text-left text-text-muted hover:text-text flex items-center gap-1"
                style="font-size:13px"
                on:click={() => (overriding = !overriding)}>
                <span>{overriding ? '▼' : '▶'}</span>
                <span>Override model / effort</span>
            </button>

            {#if overriding}
                <div class="grid grid-cols-2 gap-3">
                    <label class="flex flex-col gap-1" style="font-size:13px">
                        <span class="text-text-muted">Model</span>
                        <select
                            bind:value={chosenModel}
                            class="bg-bg border border-bg-border rounded px-2 py-1 text-text">
                            {#each MODELS as m}
                                <option value={m}>{m}</option>
                            {/each}
                        </select>
                    </label>
                    <label class="flex flex-col gap-1" style="font-size:13px">
                        <span class="text-text-muted">Effort</span>
                        <select
                            bind:value={chosenEffort}
                            class="bg-bg border border-bg-border rounded px-2 py-1 text-text">
                            {#each EFFORTS as e}
                                <option value={e}>{e}</option>
                            {/each}
                        </select>
                    </label>
                </div>
            {/if}
        {/if}

        <!-- Footer -->
        <div class="flex justify-end gap-2 pt-1">
            <button
                type="button"
                on:click={cancel}
                class="px-3 py-1 rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg"
                style="font-size:13px">
                Cancel
            </button>
            <button
                type="button"
                on:click={confirm}
                disabled={loading}
                class="px-4 py-1 rounded bg-blue-600 hover:bg-blue-500 text-white font-medium
                       disabled:opacity-50 disabled:cursor-not-allowed"
                style="font-size:13px">
                {#if overriding}
                    Start with {chosenModel}/{chosenEffort}
                {:else if rec}
                    Start with {rec.model}/{rec.effort}
                {:else}
                    Start
                {/if}
            </button>
        </div>
    </div>
</div>
