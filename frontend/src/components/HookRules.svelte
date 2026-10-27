<script lang="ts">
    // "Hook rules" block of the Waste tab (WASTE-TASKS.md WS-03): waste turned
    // into PreToolUse hook rules. A draft is only a proposal; the files it
    // would write into the repository are shown as a diff first, and nothing is
    // written before "Install". Renders nothing while [optimization] hook_rules is off.
    import { t } from '../lib/i18n';
    import {
        applyHookRule,
        fetchHookRules,
        previewHookRule,
        proposeHookRules,
        removeHookRule,
        type HookFileChange,
        type HookRule,
        type HookRulesState,
    } from '../stores/experience';

    export let project = '';

    let state: HookRulesState = { Enabled: false, Rules: [], TrialRuns: 10 };
    let error = '';
    let busy = false;
    let diffs: Record<number, HookFileChange[]> = {};
    let lastProject: string | null = null;

    $: if (project !== lastProject) {
        lastProject = project;
        diffs = {};
        void load();
    }

    $: drafts = state.Rules.filter((r) => r.Status === 'draft');
    $: installed = state.Rules.filter((r) => r.Status === 'trial' || r.Status === 'kept');
    $: past = state.Rules.filter((r) => r.Status === 'reverted');

    async function run(fn: () => Promise<unknown>) {
        busy = true;
        error = '';
        try {
            await fn();
            state = await fetchHookRules(project);
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            busy = false;
        }
    }

    async function load() {
        if (!project) return;
        await run(async () => {});
    }

    const suggest = () => run(() => proposeHookRules(project));
    const showDiff = (r: HookRule) =>
        run(async () => {
            diffs[r.ID] = await previewHookRule(r.ID);
            diffs = diffs;
        });
    const install = (r: HookRule) => run(() => applyHookRule(r.ID));
    const remove = (r: HookRule) => run(() => removeHookRule(r.ID));

    function describe(r: HookRule): string {
        return r.Param ? `${$t(`hookRules.kind.${r.Kind}`)}: ${r.Param}` : $t(`hookRules.kind.${r.Kind}`);
    }

    const f1 = (n: number) => (Math.round(n * 10) / 10).toString();
</script>

{#if state.Enabled}
    <div class="px-4 py-3 border-t border-bg-border text-xs" data-testid="hook-rules">
        <div class="flex items-center gap-3 mb-2">
            <h3 class="text-text text-sm font-medium">{$t('hookRules.title')}</h3>
            <button
                type="button"
                disabled={busy}
                on:click={suggest}
                data-testid="hook-rules-suggest"
                class="px-2 py-1 rounded border border-bg-border hover:bg-bg-elevated/60 disabled:opacity-50">
                {$t('hookRules.suggest')}
            </button>
        </div>
        <p class="text-text-muted mb-2">{$t('hookRules.intro')}</p>
        {#if error}
            <div class="text-status-error mb-2" data-testid="hook-rules-error">{error}</div>
        {/if}
        {#if state.Rules.length === 0}
            <div class="text-text-muted italic" data-testid="hook-rules-empty">{$t('hookRules.empty')}</div>
        {/if}

        {#each drafts as r (r.ID)}
            <div class="border border-bg-border rounded p-2 mb-2" data-testid="hook-rule-draft-{r.Kind}">
                <div class="text-text font-mono">{describe(r)}</div>
                {#if r.Hint}<div class="text-text-muted">{r.Hint}</div>{/if}
                {#if diffs[r.ID]}
                    {#each diffs[r.ID] as f}
                        <div class="text-text-muted mt-2">
                            {f.Delete ? $t('hookRules.fileDelete') : $t('hookRules.fileWrite')}
                            <code class="font-mono">{f.Path}</code>
                        </div>
                        <pre class="font-mono text-[11px] bg-bg-elevated rounded p-2 overflow-auto max-h-64" data-testid="hook-rule-diff">{f.Diff}</pre>
                    {/each}
                {/if}
                <div class="flex gap-2 mt-2">
                    {#if !diffs[r.ID]}
                        <button type="button" disabled={busy} on:click={() => showDiff(r)} data-testid="hook-rule-diff-btn"
                            class="px-2 py-1 rounded border border-blue-500 hover:bg-bg-elevated/60 disabled:opacity-50">
                            {$t('hookRules.showDiff')}
                        </button>
                    {:else}
                        <button type="button" disabled={busy} on:click={() => install(r)} data-testid="hook-rule-install"
                            class="px-2 py-1 rounded border border-blue-500 hover:bg-bg-elevated/60 disabled:opacity-50">
                            {$t('hookRules.install')}
                        </button>
                    {/if}
                    <button type="button" disabled={busy} on:click={() => remove(r)}
                        class="px-2 py-1 rounded border border-bg-border hover:bg-bg-elevated/60 disabled:opacity-50">
                        {$t('hookRules.dismiss')}
                    </button>
                </div>
            </div>
        {/each}

        {#each installed as r (r.ID)}
            <div class="border border-bg-border rounded p-2 mb-2" data-testid="hook-rule-installed-{r.Kind}">
                <div class="flex items-center gap-2">
                    <span class="text-text font-mono">{describe(r)}</span>
                    <span class="text-text-muted">— {$t(`hookRules.status.${r.Status}`, { runs: state.TrialRuns })}</span>
                    <button type="button" disabled={busy} on:click={() => remove(r)} data-testid="hook-rule-remove"
                        class="ml-auto px-2 py-1 rounded border border-bg-border hover:bg-bg-elevated/60 disabled:opacity-50">
                        {$t('hookRules.remove')}
                    </button>
                </div>
                <div class="text-text-muted">
                    {$t('hookRules.effect', {
                        stepsBefore: f1(r.Effect.steps_before),
                        stepsAfter: f1(r.Effect.steps_after),
                        errorsBefore: f1(r.Effect.errors_before),
                        errorsAfter: f1(r.Effect.errors_after),
                        runs: r.Effect.runs_after,
                    })}
                </div>
            </div>
        {/each}

        {#each past as r (r.ID)}
            <div class="text-text-muted mb-1" data-testid="hook-rule-reverted-{r.Kind}">
                {describe(r)} — {$t(`hookRules.why.${r.Why?.code ?? 'reverted_manual'}`)}
            </div>
        {/each}
    </div>
{/if}
