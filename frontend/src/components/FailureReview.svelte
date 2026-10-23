<script lang="ts">
    // "Failures" tab of ExperiencePanel (LEARN-TASKS.md LN-39): recurring
    // errors turned into CLAUDE.md rules. Drafts are reviewed and edited here;
    // nothing reaches the repository before "Accept into CLAUDE.md".
    import { formatCost, formatTime } from '../lib/formatters';
    import { t } from '../lib/i18n';
    import { EventsOn } from '../../wailsjs/runtime/runtime';
    import { onMount } from 'svelte';
    import {
        acceptFailureRule,
        dismissFailureRule,
        draftFailureRules,
        fetchFailureRules,
        restoreFailureRule,
        revertFailureRule,
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
    $: applied = rules.filter((r) => r.Status === 'trial' || r.Status === 'kept' || r.Status === 'reverted');

    // The autopilot (LN-40) sends skills:changed when it applied or switched off a rule.
    onMount(() => {
        const off = EventsOn('skills:changed', (evt: { project: string }) => {
            if (evt?.project === project) void load();
        });
        return () => off?.();
    });

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

    async function act(r: FailureRule, what: 'revert' | 'restore' | 'move') {
        busy[r.ID] = true;
        busy = busy;
        error = '';
        try {
            if (what === 'revert') await revertFailureRule(r.ID);
            else if (what === 'restore') await restoreFailureRule(r.ID);
            else await acceptFailureRule(r.ID, r.RuleText);
            await load();
        } catch (e) {
            error = String(e);
        } finally {
            busy[r.ID] = false;
            busy = busy;
        }
    }

    function appliedStatus(r: FailureRule): string {
        if (r.Status === 'trial') {
            const n = Math.min(r.Effect?.RunsAfter ?? 0, r.TrialRuns || 10);
            return $t('experiencePanel.failures.statusTrial', { n, total: r.TrialRuns || 10 });
        }
        return $t(r.Status === 'kept' ? 'experiencePanel.failures.statusKept' : 'experiencePanel.failures.statusReverted');
    }

    function appliedWhy(r: FailureRule): string {
        const w = r.Why;
        if (!w) return '';
        const key = 'experiencePanel.failures.why.' + w.code;
        const text = $t(key);
        const head = text === key ? w.code : text;
        return head + ': ' + $t('experiencePanel.failures.effect', {
            hb: w.hits_before, rb: w.runs_before, ha: w.hits_after, ra: w.runs_after,
        });
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

        {#if applied.length > 0}
            <h3 class="font-medium">{$t('experiencePanel.failures.applied', { n: applied.length })}</h3>
            {#each applied as r (r.ID)}
                <div class="border border-bg-border rounded p-3 space-y-1" data-testid="failure-applied">
                    <div>{r.RuleText}</div>
                    <div class="text-xs text-text-muted" data-testid="failure-applied-status">
                        {appliedStatus(r)}{#if r.Why} · {appliedWhy(r)}{/if}
                    </div>
                    <div class="flex gap-2">
                        {#if r.Status === 'reverted'}
                            <button
                                type="button"
                                disabled={busy[r.ID]}
                                on:click={() => act(r, 'restore')}
                                class="px-2 py-1 rounded border border-bg-border hover:bg-bg-elevated/60 disabled:opacity-50">
                                {$t('experiencePanel.failures.restore')}
                            </button>
                        {:else}
                            <button
                                type="button"
                                disabled={busy[r.ID]}
                                on:click={() => act(r, 'revert')}
                                class="px-2 py-1 rounded border border-bg-border hover:bg-bg-elevated/60 disabled:opacity-50">
                                {$t('experiencePanel.failures.revert')}
                            </button>
                            <button
                                type="button"
                                disabled={busy[r.ID]}
                                on:click={() => act(r, 'move')}
                                class="px-2 py-1 rounded border border-blue-500 hover:bg-bg-elevated/60 disabled:opacity-50">
                                {$t('experiencePanel.failures.moveToClaudeMd')}
                            </button>
                        {/if}
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
