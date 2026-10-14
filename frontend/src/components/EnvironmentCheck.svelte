<script lang="ts">
    // Settings → General → Environment: the programs the manager and its
    // sessions rely on, and how to install each missing one here
    // (App.CheckEnvironment, internal/envcheck).
    import { onMount, onDestroy } from 'svelte';
    import { CheckEnvironment } from '../../wailsjs/go/main/App';
    import { t } from '../lib/i18n';
    import { copyText, COPY_FEEDBACK_MS } from '../lib/clipboard';

    interface Tool {
        ID: string;
        Name: string;
        Level: 'required' | 'recommended' | 'build' | 'optional';
        Found: boolean;
        Path: string;
        Version: string;
        Want: string;
        Mismatch: boolean;
        Install: string;
        InstallVia: string;
        Note: string;
    }
    interface Report {
        OS: string;
        PackageManager: string;
        Tools: Tool[];
    }

    let report: Report | null = null;
    let loading = false;
    let error = '';
    let copied = '';
    let copyTimer: ReturnType<typeof setTimeout> | undefined;

    async function run() {
        loading = true;
        error = '';
        try {
            report = (await CheckEnvironment()) as unknown as Report;
        } catch (e) {
            error = String(e);
        } finally {
            loading = false;
        }
    }

    async function copy(tool: Tool) {
        copied = (await copyText(tool.Install)) ? tool.ID : '';
        clearTimeout(copyTimer);
        copyTimer = setTimeout(() => (copied = ''), COPY_FEEDBACK_MS);
    }

    // A problem is a missing tool or a wrong version; optional tools are
    // listed but never count.
    const isProblem = (x: Tool) => (!x.Found || x.Mismatch) && x.Level !== 'optional';
    $: problems = report ? report.Tools.filter(isProblem) : [];
    $: missingRequired = problems.filter((x) => x.Level === 'required').length;

    const levelClass: Record<string, string> = {
        required: 'text-status-error',
        recommended: 'text-status-waiting',
        build: 'text-text-muted',
        optional: 'text-text-dim',
    };

    onMount(run);
    onDestroy(() => clearTimeout(copyTimer));
</script>

<section data-testid="env-check">
    <div class="flex items-center gap-3 mb-2">
        <h3 class="text-text font-semibold text-sm">{$t('settings.env.heading')}</h3>
        <button
            type="button"
            data-testid="env-check-run"
            class="text-xs px-2 py-0.5 rounded border border-bg-border text-text-muted hover:text-text disabled:opacity-50"
            disabled={loading}
            on:click={run}>{loading ? $t('settings.env.checking') : $t('settings.env.recheck')}</button>
        {#if report && !loading}
            {#if problems.length === 0}
                <span class="text-xs text-status-working" data-testid="env-check-summary">{$t('settings.env.allOk')}</span>
            {:else}
                <span class="text-xs {missingRequired ? 'text-status-error' : 'text-status-waiting'}" data-testid="env-check-summary">
                    {$t('settings.env.problems', { n: problems.length })}
                </span>
            {/if}
        {/if}
    </div>
    <p class="text-text-dim text-xs mb-2">{$t('settings.env.description')}</p>

    {#if error}
        <div class="text-status-error text-xs">{error}</div>
    {:else if report}
        <table class="w-full text-xs">
            <tbody>
                {#each report.Tools as tool (tool.ID)}
                    <tr class="border-t border-bg-border align-top" data-testid="env-tool-{tool.ID}">
                        <td class="py-1 pr-2 w-5">
                            {#if tool.Found && !tool.Mismatch}
                                <span class="text-status-working" title={$t('settings.env.found')}>✓</span>
                            {:else if tool.Found}
                                <span class="text-status-waiting" title={$t('settings.env.mismatch')}>⚠</span>
                            {:else}
                                <span class={levelClass[tool.Level]} title={$t('settings.env.missing')}>✖</span>
                            {/if}
                        </td>
                        <td class="py-1 pr-3 whitespace-nowrap">
                            <div class="text-text">{tool.Name}</div>
                            <div class={levelClass[tool.Level]}>{$t(`settings.env.level.${tool.Level}`)}</div>
                        </td>
                        <td class="py-1 pr-3 text-text-muted">
                            <div>{$t(`settings.env.tool.${tool.ID}`)}</div>
                            {#if tool.Found}
                                <div class="text-text-dim font-mono break-all" title={tool.Path}>
                                    {tool.Version || tool.Path}
                                    {#if tool.Mismatch}
                                        <span class="text-status-waiting">— {$t('settings.env.want', { want: tool.Want })}</span>
                                    {/if}
                                </div>
                            {/if}
                            {#if tool.Note}
                                <div class="text-text-dim">{$t(`settings.env.note.${tool.Note}`)}</div>
                            {/if}
                            {#if (!tool.Found || tool.Mismatch) && tool.Install}
                                <div class="flex items-center gap-2 mt-1">
                                    <code class="bg-bg border border-bg-border rounded px-1.5 py-0.5 text-text font-mono break-all"
                                        data-testid="env-install-{tool.ID}">{tool.Install}</code>
                                    <button
                                        type="button"
                                        class="shrink-0 px-1.5 py-0.5 rounded border border-bg-border text-text-muted hover:text-text"
                                        title={$t('settings.env.copy')}
                                        on:click={() => copy(tool)}>{copied === tool.ID ? '✓' : '⧉'}</button>
                                    <span class="text-text-dim whitespace-nowrap">{$t(`settings.env.via.${tool.InstallVia}`)}</span>
                                </div>
                            {:else if !tool.Found && !tool.Note}
                                <div class="text-text-dim">{$t('settings.env.noInstall')}</div>
                            {/if}
                        </td>
                    </tr>
                {/each}
            </tbody>
        </table>
    {:else if loading}
        <div class="text-text-muted text-xs italic">{$t('settings.env.checking')}</div>
    {/if}
</section>
