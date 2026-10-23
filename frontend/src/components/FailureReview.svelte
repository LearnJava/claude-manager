<script lang="ts">
    // "Failures" tab of ExperiencePanel (LEARN-TASKS.md LN-39): recurring
    // errors turned into CLAUDE.md rules. Drafts are reviewed and edited here;
    // nothing reaches the repository before "Accept into CLAUDE.md".
    import { formatCost, formatTime } from '../lib/formatters';
    import { t } from '../lib/i18n';
    import {
        acceptFailureRule,
        dismissFailureRule,
        draftFailureRules,
        fetchFailureRules,
        type FailureRule,
    } from '../stores/experience';

    export let project = '';

    let rules: FailureRule[] = [];
    let pending = 0;
    let loading = false;
    let drafting = false;
    let error = '';
    let lastCost: number | null = null;
    let texts: Record<number, string> = {};
    let busy: Record<number, boolean> = {};
    let lastProject: string | null = null;

    $: if (project !== lastProject) {
        lastProject = project;
        void load();
    }

    $: drafts = rules.filter((r) => r.Status === 'draft');
    $: accepted = rules.filter((r) => r.Status === 'accepted');
    $: dismissed = rules.filter((r) => r.Status === 'dismissed');

    export async function load() {
        if (!project) {
            rules = [];
            pending = 0;
            return;
        }
        loading = true;
        try {
            const st = await fetchFailureRules(project);
            rules = st.Rules ?? [];
            pending = st.Pending ?? 0;
            for (const r of rules) {
                if (texts[r.ID] === undefined) texts[r.ID] = r.RuleText;
            }
            texts = texts;
        } catch (e) {
            error = String(e);
        } finally {
            loading = false;
        }
    }

    async function suggest() {
        drafting = true;
        error = '';
        try {
            const made = await draftFailureRules(project, 3);
            lastCost = made.reduce((s, r) => s + (r.CostUSD ?? 0), 0);
        } catch (e) {
            error = String(e);
        } finally {
            drafting = false;
        }
        await load();
    }

    async function decide(r: FailureRule, accept: boolean) {
        busy[r.ID] = true;
        busy = busy;
        error = '';
        try {
            if (accept) await acceptFailureRule(r.ID, texts[r.ID] ?? r.RuleText);
            else await dismissFailureRule(r.ID);
            await load();
        } catch (e) {
            error = String(e);
        } finally {
            busy[r.ID] = false;
            busy = busy;
        }
    }

    function clip(s: string, n = 200): string {
        return s.length > n ? s.slice(0, n) + '…' : s;
    }
</script>

<div class="space-y-4 text-sm" data-testid="failures-tab">
    {#if !project}
        <div class="text-text-muted italic py-10 text-center">{$t('experiencePanel.noProject')}</div>
    {:else}
        <div class="flex items-center gap-3 flex-wrap">
            <span data-testid="failures-pending">{$t('experiencePanel.failures.pending', { n: pending })}</span>
            <button
                type="button"
                disabled={drafting}
                on:click={suggest}
                class="px-2 py-1 rounded border border-bg-border hover:bg-bg-elevated/60 disabled:opacity-50">
                {drafting ? $t('experiencePanel.failures.drafting') : $t('experiencePanel.failures.suggest')}
            </button>
            {#if lastCost !== null && !drafting}
                <span class="text-text-muted text-xs">
                    {$t('experiencePanel.failures.cost', { cost: formatCost(lastCost) })}
                </span>
            {/if}
        </div>

        {#if error}
            <div class="text-status-error" data-testid="failures-error">
                {$t('experiencePanel.failures.error', { error })}
            </div>
        {/if}

        {#if loading && rules.length === 0}
            <div class="text-text-muted italic">{$t('experiencePanel.failures.loading')}</div>
        {:else if rules.length === 0}
            <div class="text-text-muted italic py-6 text-center">{$t('experiencePanel.failures.empty')}</div>
        {/if}

        {#if drafts.length > 0}
            <h3 class="font-medium">{$t('experiencePanel.failures.drafts', { n: drafts.length })}</h3>
            {#each drafts as r (r.ID)}
                <div class="border border-bg-border rounded p-3 space-y-2" data-testid="failure-draft">
                    <div class="flex items-baseline gap-2">
                        <code class="font-mono text-xs break-all">{r.ErrorKey}</code>
                        <span class="text-text-muted text-xs">
                            {$t('experiencePanel.failures.inRuns', { n: r.DistinctRuns })}
                        </span>
                    </div>
                    {#each r.Examples ?? [] as ex}
                        <div class="font-mono text-xs space-y-0.5">
                            <div class="text-status-error break-all">
                                {$t('experiencePanel.failures.failed')}: {clip(ex.failed_arg)}
                            </div>
                            <div class="text-text-muted break-all">{clip(ex.err_text)}</div>
                            <div class="text-green-500 break-all">
                                {$t('experiencePanel.failures.fixed')}: {clip(ex.fixed_arg)}
                            </div>
                        </div>
                    {/each}
                    <textarea
                        rows="3"
                        bind:value={texts[r.ID]}
                        class="w-full bg-bg-elevated border border-bg-border rounded p-2 text-xs"></textarea>
                    <div class="flex gap-2">
                        <button
                            type="button"
                            disabled={busy[r.ID]}
                            on:click={() => decide(r, true)}
                            class="px-2 py-1 rounded border border-blue-500 hover:bg-bg-elevated/60 disabled:opacity-50">
                            {$t('experiencePanel.failures.accept')}
                        </button>
                        <button
                            type="button"
                            disabled={busy[r.ID]}
                            on:click={() => decide(r, false)}
                            class="px-2 py-1 rounded border border-bg-border hover:bg-bg-elevated/60 disabled:opacity-50">
                            {$t('experiencePanel.failures.dismiss')}
                        </button>
                    </div>
                </div>
            {/each}
        {/if}

        {#if accepted.length > 0}
            <h3 class="font-medium">{$t('experiencePanel.failures.accepted', { n: accepted.length })}</h3>
            {#each accepted as r (r.ID)}
                <div class="border border-bg-border rounded p-3 space-y-1" data-testid="failure-accepted">
                    <div>{r.RuleText}</div>
                    <div class="text-xs text-text-muted">
                        {#if r.DecidedAt}{$t('experiencePanel.failures.decided', { date: formatTime(r.DecidedAt) })} · {/if}
                        {$t('experiencePanel.failures.effect', {
                            hb: r.Effect?.HitsBefore ?? 0,
                            rb: r.Effect?.RunsBefore ?? 0,
                            ha: r.Effect?.HitsAfter ?? 0,
                            ra: r.Effect?.RunsAfter ?? 0,
                        })}
                    </div>
                </div>
            {/each}
        {/if}

        {#if dismissed.length > 0}
            <details data-testid="failure-dismissed">
                <summary class="cursor-pointer text-text-muted">
                    {$t('experiencePanel.failures.dismissed', { n: dismissed.length })}
                </summary>
                {#each dismissed as r (r.ID)}
                    <div class="mt-2 text-xs text-text-muted">
                        <code class="font-mono">{r.ErrorKey}</code> — {r.RuleText}
                    </div>
                {/each}
            </details>
        {/if}
    {/if}
</div>
