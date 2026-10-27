<script lang="ts">
    // "Suggestions" block of the Waste tab (WASTE-TASKS.md WS-07): work the
    // repository shows without a model — stale TODO/FIXME, red gates, code/doc
    // drift, broken queue pointers, abandoned branches and folders. Scanning is
    // read only; a ticked entry is written as a task and its pointer appended
    // to the chosen queue only on "Add to queue". Renders nothing while
    // [optimization] repo_suggestions is off.
    import { t } from '../lib/i18n';
    import {
        acceptSuggestions,
        fetchSuggestions,
        type RepoSuggestion,
        type SuggestionsState,
    } from '../stores/experience';

    export let project = '';

    let state: SuggestionsState = { Enabled: false, Items: [], QueueFiles: [], TaskFile: '' };
    let error = '';
    let note = '';
    let busy = false;
    let scanned = false;
    let ticked: Record<string, boolean> = {};
    let queue = '';
    let lastProject: string | null = null;

    $: if (project !== lastProject) {
        lastProject = project;
        void scan();
    }

    $: keys = state.Items.filter((i) => ticked[i.Key]).map((i) => i.Key);

    async function scan() {
        if (!project) return;
        busy = true;
        error = '';
        try {
            state = await fetchSuggestions(project);
            ticked = {};
            scanned = true;
            if (!state.QueueFiles.includes(queue)) queue = state.QueueFiles[0] ?? '';
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            busy = false;
        }
    }

    async function add() {
        busy = true;
        error = '';
        note = '';
        try {
            const pointers = await acceptSuggestions(project, queue, keys);
            note = $t('suggestions.added', { count: pointers.length, file: queue, pointers: pointers.join(', ') });
            state = await fetchSuggestions(project);
            ticked = {};
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            busy = false;
        }
    }

    function toggle(i: RepoSuggestion) {
        ticked[i.Key] = !ticked[i.Key];
        ticked = ticked;
    }
</script>

{#if state.Enabled}
    <div class="px-4 py-3 border-t border-bg-border text-xs" data-testid="suggestions">
        <div class="flex items-center gap-3 mb-2">
            <h3 class="text-text text-sm font-medium">{$t('suggestions.title')}</h3>
            <button
                type="button"
                disabled={busy}
                on:click={scan}
                data-testid="suggestions-scan"
                class="px-2 py-1 rounded border border-bg-border hover:bg-bg-elevated/60 disabled:opacity-50">
                {$t('suggestions.scan')}
            </button>
        </div>
        <p class="text-text-muted mb-2">{$t('suggestions.intro')}</p>
        {#if error}
            <div class="text-status-error mb-2" data-testid="suggestions-error">{error}</div>
        {/if}
        {#if note}
            <div class="text-text mb-2" data-testid="suggestions-note">{note}</div>
        {/if}
        {#if scanned && state.Items.length === 0}
            <div class="text-text-muted italic" data-testid="suggestions-empty">{$t('suggestions.empty')}</div>
        {/if}

        {#each state.Items as i (i.Key)}
            <label class="flex gap-2 border border-bg-border rounded p-2 mb-2 cursor-pointer" data-testid="suggestion-{i.Kind}">
                <input
                    type="checkbox"
                    checked={!!ticked[i.Key]}
                    on:change={() => toggle(i)}
                    data-testid="suggestion-check"
                    class="mt-0.5" />
                <span class="min-w-0">
                    <span class="text-text-muted">{$t(`suggestions.kind.${i.Kind}`)}</span>
                    <span class="block text-text font-mono">{i.Title}</span>
                    <span class="block text-text-muted whitespace-pre-line">{i.Detail}</span>
                </span>
            </label>
        {/each}

        {#if state.Items.length > 0}
            {#if state.QueueFiles.length === 0}
                <div class="text-text-muted italic" data-testid="suggestions-no-queue">{$t('suggestions.noQueue')}</div>
            {:else}
                <div class="flex items-center gap-2">
                    <span class="text-text-muted">{$t('suggestions.queue')}</span>
                    <select
                        bind:value={queue}
                        data-testid="suggestions-queue"
                        class="px-2 py-1 rounded border border-bg-border bg-bg-elevated text-text">
                        {#each state.QueueFiles as q}
                            <option value={q}>{q}</option>
                        {/each}
                    </select>
                    <button
                        type="button"
                        disabled={busy || keys.length === 0}
                        on:click={add}
                        data-testid="suggestions-add"
                        class="px-2 py-1 rounded border border-blue-500 hover:bg-bg-elevated/60 disabled:opacity-50">
                        {$t('suggestions.add', { count: keys.length })}
                    </button>
                </div>
            {/if}
        {/if}
    </div>
{/if}
