<script lang="ts">
    // "Learned files" block of the Waste tab (WASTE-TASKS.md WS-09): the skills,
    // rules and bans kept in the database next to the plain files in
    // <project>/.claude-manager/learned/. Comparing is read only; files are
    // written or removed, and lessons imported or retired, only for the entries
    // ticked and confirmed with "Apply". A conflict (both sides changed) needs a
    // choice before it can be applied. Renders nothing while
    // [optimization] learned_files is off.
    import { t } from '../lib/i18n';
    import {
        applyLearnedFiles,
        fetchLearnedFiles,
        type LearnedItem,
        type LearnedPick,
        type LearnedState,
    } from '../stores/experience';

    export let project = '';

    let state: LearnedState = { Enabled: false, Dir: '', Items: [], Problems: [] };
    let error = '';
    let note = '';
    let failed: string[] = [];
    let busy = false;
    let scanned = false;
    let ticked: Record<string, boolean> = {};
    let choice: Record<string, string> = {};
    let lastProject: string | null = null;

    const id = (i: LearnedItem) => `${i.Kind}|${i.Key}`;

    $: if (project !== lastProject) {
        lastProject = project;
        void scan();
    }

    $: picked = state.Items.filter((i) => ticked[id(i)]);
    $: unsettled = picked.some((i) => i.Action === 'conflict' && !choice[id(i)]);
    $: imports = state.Items.filter((i) => i.Action === 'import_new' || i.Action === 'import_upd').length;

    async function scan() {
        if (!project) return;
        busy = true;
        error = '';
        try {
            state = await fetchLearnedFiles(project);
            ticked = {};
            choice = {};
            scanned = true;
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            busy = false;
        }
    }

    async function apply() {
        busy = true;
        error = '';
        note = '';
        failed = [];
        try {
            const picks: LearnedPick[] = picked.map((i) => ({
                Kind: i.Kind,
                Key: i.Key,
                Resolution: i.Action === 'conflict' ? choice[id(i)] : '',
            }));
            const r = await applyLearnedFiles(project, picks);
            note = $t('learned.applied', { count: r.Done.length });
            failed = r.Failed;
            state = await fetchLearnedFiles(project);
            ticked = {};
            choice = {};
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            busy = false;
        }
    }

    function toggle(i: LearnedItem) {
        ticked[id(i)] = !ticked[id(i)];
        ticked = ticked;
    }
</script>

{#if state.Enabled}
    <div class="px-4 py-3 border-t border-bg-border text-xs" data-testid="learned-files">
        <div class="flex items-center gap-3 mb-2">
            <h3 class="text-text text-sm font-medium">{$t('learned.title')}</h3>
            <button
                type="button"
                disabled={busy}
                on:click={scan}
                data-testid="learned-scan"
                class="px-2 py-1 rounded border border-bg-border hover:bg-bg-elevated/60 disabled:opacity-50">
                {$t('learned.scan')}
            </button>
        </div>
        <p class="text-text-muted mb-2">{$t('learned.intro', { dir: state.Dir })}</p>
        {#if imports > 0}
            <div class="text-text mb-2" data-testid="learned-offer">{$t('learned.offer', { count: imports })}</div>
        {/if}
        {#if error}
            <div class="text-status-error mb-2" data-testid="learned-error">{error}</div>
        {/if}
        {#if note}
            <div class="text-text mb-2" data-testid="learned-note">{note}</div>
        {/if}
        {#each failed as f}
            <div class="text-status-error mb-1" data-testid="learned-failed">{f}</div>
        {/each}
        {#each state.Problems as p}
            <div class="text-text-muted mb-1" data-testid="learned-problem">{p}</div>
        {/each}
        {#if scanned && state.Items.length === 0}
            <div class="text-text-muted italic" data-testid="learned-empty">{$t('learned.empty')}</div>
        {/if}

        {#each state.Items as i (id(i))}
            <div class="flex gap-2 border border-bg-border rounded p-2 mb-2" data-testid="learned-{i.Action}">
                <input
                    type="checkbox"
                    checked={!!ticked[id(i)]}
                    on:change={() => toggle(i)}
                    data-testid="learned-check"
                    class="mt-0.5" />
                <span class="min-w-0 flex-1">
                    <span class="text-text-muted">{$t(`learned.kind.${i.Kind}`)} · {$t(`learned.action.${i.Action}`)}</span>
                    <span class="block text-text font-mono break-words">{i.Title}</span>
                    <span class="block text-text-muted">
                        {$t('learned.status', { db: i.DBStatus || '—', file: i.FileStatus || '—' })}
                        {#if i.Detail}· {i.Detail}{/if}
                    </span>
                    {#if i.Action === 'conflict'}
                        <select
                            value={choice[id(i)] ?? ''}
                            on:change={(e) => {
                                choice[id(i)] = e.currentTarget.value;
                                choice = choice;
                            }}
                            data-testid="learned-choice"
                            class="mt-1 px-2 py-1 rounded border border-bg-border bg-bg-elevated text-text">
                            <option value="">{$t('learned.choose')}</option>
                            <option value="file">{$t('learned.useFile')}</option>
                            <option value="db">{$t('learned.useDb')}</option>
                        </select>
                    {/if}
                </span>
            </div>
        {/each}

        {#if state.Items.length > 0}
            <button
                type="button"
                disabled={busy || picked.length === 0 || unsettled}
                on:click={apply}
                data-testid="learned-apply"
                class="px-2 py-1 rounded border border-blue-500 hover:bg-bg-elevated/60 disabled:opacity-50">
                {$t('learned.apply', { count: picked.length })}
            </button>
        {/if}
    </div>
{/if}
