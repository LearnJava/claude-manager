<script lang="ts">
    import { createEventDispatcher } from 'svelte';
    import { t } from '../lib/i18n';
    import { GetConfig, UpdateConfig, RestartSession } from '../../wailsjs/go/main/App';
    import { initProjects } from '../stores/projects';
    import { refreshSessions, sessions } from '../stores/sessions';
    import {
        describeSessionFeatures,
        fieldTarget,
        GROUP_ORDER,
        LIVE,
        type FeatureField,
        type FeatureItem,
    } from '../lib/sessionFeatures';

    export let project: string;
    export let sessionName: string;

    const dispatch = createEventDispatcher<{ close: void }>();

    // draft is the whole AppConfig as GetConfig returned it. Every edit is
    // saved at once (no Save button): the whole config goes through
    // UpdateConfig exactly like the Settings dialog sends it, so project
    // overlays are written the same way and the manager gets the new values
    // immediately. Saves are serialized through `queue` so a fast second
    // click never races the first one.
    let draft: any = null;
    let loading = true;
    let saving = false;
    let error = '';
    let savedKey = '';      // feature key whose save just finished, for the ✓
    let onlyOff = false;
    let queue: Promise<void> = Promise.resolve();
    // Set when a start-time setting was saved while the session runs.
    let needsRestart = false;
    let restarting = false;

    $: sessionId = `${project}/${sessionName}`;
    $: status = $sessions[sessionId]?.status ?? 'idle';
    $: running = status !== 'idle' && status !== 'error';

    async function load() {
        loading = true;
        error = '';
        try {
            draft = await GetConfig();
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            loading = false;
        }
    }

    load();

    function getValue(fl: FeatureField): any {
        return fieldTarget(draft, project, sessionName, fl.target)?.[fl.prop];
    }

    function setValue(key: string, fl: FeatureField, v: any) {
        const target = fieldTarget(draft, project, sessionName, fl.target);
        if (!target) return;
        target[fl.prop] = v;
        draft = draft;
        save(key);
    }

    function onInput(key: string, fl: FeatureField, e: Event) {
        const el = e.currentTarget as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement;
        switch (fl.kind) {
            case 'bool':
                setValue(key, fl, (el as HTMLInputElement).checked);
                break;
            case 'number': {
                const n = parseFloat(el.value);
                setValue(key, fl, isFinite(n) && n > 0 ? n : 0);
                break;
            }
            case 'list':
                setValue(key, fl, el.value.split(/[,\n]/).map((x) => x.trim()).filter(Boolean));
                break;
            default:
                setValue(key, fl, el.value);
        }
    }

    function listText(v: unknown): string {
        return Array.isArray(v) ? v.join(', ') : '';
    }

    function save(key: string) {
        const snapshot = JSON.parse(JSON.stringify(draft));
        queue = queue.then(async () => {
            saving = true;
            error = '';
            savedKey = '';
            try {
                await UpdateConfig(snapshot);
                await initProjects();
                await refreshSessions();
                // Re-pull so the popup shows the canonical (defaulted) values.
                draft = await GetConfig();
                savedKey = key;
                if (running && !LIVE.has(key)) needsRestart = true;
            } catch (e: any) {
                error = $t('sessionFeatures.saveFailed', { error: e?.message ?? String(e) });
                draft = await GetConfig().catch(() => draft);
            } finally {
                saving = false;
            }
        });
    }

    async function restartNow() {
        if (restarting) return;
        restarting = true;
        error = '';
        try {
            await RestartSession(sessionId);
            needsRestart = false;
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            restarting = false;
        }
    }

    async function close() {
        await queue; // never drop an in-flight save
        dispatch('close');
    }

    $: items = draft ? describeSessionFeatures(draft, project, sessionName) : null;
    $: enabledCount = items?.filter((i) => i.enabled).length ?? 0;
    $: warnCount = items?.filter((i) => i.warning).length ?? 0;
    $: visible = (items ?? []).filter((i) => !onlyOff || !i.enabled || i.warning);
    $: groups = GROUP_ORDER
        .map((g) => ({ group: g, items: visible.filter((i) => i.group === g) }))
        .filter((g) => g.items.length > 0);

    const inputCls = 'bg-bg border border-bg-border rounded px-1.5 py-0.5 text-text';

    // Detailed help ("i" next to a title): featureHelp.<key>.body/.example.
    let helpItem: FeatureItem | null = null;
    $: helpParas = helpItem ? $t(`featureHelp.${helpItem.key}.body`).split(/\n\s*\n/) : [];
    $: helpExample = helpItem ? $t(`featureHelp.${helpItem.key}.example`) : '';

    // Splits text on `backticks` so config keys render as code without {@html}.
    function codeParts(text: string): { code: boolean; text: string }[] {
        return text.split('`').map((part, i) => ({ code: i % 2 === 1, text: part }));
    }

    function onEscape(e: KeyboardEvent) {
        if (e.key !== 'Escape') return;
        if (helpItem) {
            e.stopPropagation();
            helpItem = null;
        } else {
            close();
        }
    }
</script>

<!-- Escape closes from anywhere, including focus inside the panel. -->
<svelte:window on:keydown|capture={onEscape} />

<!-- Backdrop -->
<div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    on:click={close}
    on:keydown={(e) => e.key === 'Escape' && close()}
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    data-testid="session-features">

    <div
        class="bg-bg-panel border border-bg-border rounded-lg shadow-2xl w-[760px] max-w-[95vw] max-h-[88vh] flex flex-col"
        role="document"
        on:click|stopPropagation
        on:keydown|stopPropagation>

        <!-- Header -->
        <div class="flex items-center justify-between px-5 pt-4 pb-3 border-b border-bg-border">
            <div class="min-w-0">
                <span class="text-text font-semibold">{$t('sessionFeatures.title')}</span>
                <span class="text-text-muted ml-2" style="font-size:13px">{project}/{sessionName}</span>
            </div>
            <button class="text-text-muted hover:text-text" on:click={close} type="button">✕</button>
        </div>

        <!-- Summary + filter -->
        {#if items}
            <div class="flex items-center gap-4 px-5 py-2 text-xs text-text-muted border-b border-bg-border">
                <span class="whitespace-nowrap" data-testid="features-summary">
                    {$t('sessionFeatures.summary', { on: enabledCount, total: items.length })}
                </span>
                {#if warnCount > 0}
                    <span class="text-status-ratelimit whitespace-nowrap">⚠ {$t('sessionFeatures.warnCount', { n: warnCount })}</span>
                {/if}
                <label class="ml-auto flex items-center gap-1.5 cursor-pointer select-none">
                    <input type="checkbox" bind:checked={onlyOff} />
                    {$t('sessionFeatures.onlyOff')}
                </label>
            </div>
        {/if}

        <!-- Body -->
        <div class="overflow-y-auto px-5 py-3 flex flex-col gap-4">
            {#if loading && !draft}
                <div class="text-text-muted text-sm py-4 text-center">{$t('sessionFeatures.loading')}</div>
            {:else if !items}
                {#if !error}
                    <div class="text-text-muted text-sm py-2">{$t('sessionFeatures.notFound')}</div>
                {/if}
            {:else}
                {#each groups as g (g.group)}
                    <section>
                        <h3 class="text-text-muted uppercase tracking-wide mb-1.5" style="font-size:11px">
                            {$t(`sessionFeatures.group.${g.group}`)}
                        </h3>
                        <ul class="flex flex-col gap-1.5">
                            {#each g.items as it (it.key)}
                                <li
                                    class="bg-bg-elevated border rounded px-3 py-2
                                           {it.warning ? 'border-status-ratelimit/60' : 'border-bg-border'}"
                                    data-testid="feature-{it.key}"
                                    data-enabled={it.enabled}>
                                    <div class="flex items-center gap-2 text-sm">
                                        <span
                                            class="shrink-0 px-1.5 rounded font-semibold
                                                   {it.enabled ? 'bg-status-working/20 text-status-working' : 'bg-bg-border text-text-muted'}"
                                            style="font-size:11px" data-testid="feature-state">
                                            {it.enabled ? $t('sessionFeatures.on') : $t('sessionFeatures.off')}
                                        </span>
                                        <span class="text-text font-medium">{$t(`sessionFeatures.${it.key}.title`)}</span>
                                        <button
                                            type="button"
                                            class="shrink-0 w-4 h-4 rounded-full border border-text-muted/60 text-text-muted
                                                   hover:text-text hover:border-text leading-none font-semibold"
                                            style="font-size:10px"
                                            title={$t('featureHelp.open')}
                                            aria-label={$t('featureHelp.open')}
                                            data-testid="feature-help"
                                            on:click={() => (helpItem = it)}>i</button>
                                        {#if savedKey === it.key}
                                            <span class="text-status-working shrink-0" style="font-size:11px"
                                                  data-testid="feature-saved">✓ {$t('sessionFeatures.savedShort')}</span>
                                        {/if}
                                        {#if it.value}
                                            <span class="text-text-muted font-mono truncate" style="font-size:12px" title={it.value}>{it.value}</span>
                                        {/if}
                                    </div>
                                    <div class="text-text-muted mt-1" style="font-size:12px">
                                        {$t(`sessionFeatures.${it.key}.desc`)}
                                    </div>
                                    {#if it.warning}
                                        <div class="text-status-ratelimit mt-1" style="font-size:12px">
                                            ⚠ {$t(`sessionFeatures.warn.${it.warning}`)}
                                        </div>
                                    {/if}

                                    <!-- Editors -->
                                    <div class="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1.5" style="font-size:12px">
                                        {#each it.fields as fl (fl.prop)}
                                            {@const v = getValue(fl)}
                                            <label class="flex items-center gap-1.5 text-text-muted {fl.kind === 'textarea' || fl.kind === 'list' ? 'w-full' : ''}">
                                                {#if fl.kind === 'bool'}
                                                    <input type="checkbox" checked={!!v} data-testid="field-{fl.label}"
                                                           on:change={(e) => onInput(it.key, fl, e)} />
                                                    <span class="font-mono">{fl.label}</span>
                                                {:else}
                                                    <span class="font-mono shrink-0">{fl.label}</span>
                                                    {#if fl.kind === 'select'}
                                                        <select class={inputCls} value={v ?? ''} data-testid="field-{fl.label}"
                                                                on:change={(e) => onInput(it.key, fl, e)}>
                                                            {#each fl.options ?? [] as opt}
                                                                <option value={opt}>{opt}</option>
                                                            {/each}
                                                        </select>
                                                    {:else if fl.kind === 'number'}
                                                        <input type="number" min="0" step={fl.step ?? 1} class="{inputCls} w-28"
                                                               value={v ?? 0} data-testid="field-{fl.label}"
                                                               on:change={(e) => onInput(it.key, fl, e)} />
                                                    {:else if fl.kind === 'textarea'}
                                                        <textarea rows="2" class="{inputCls} flex-1 font-mono"
                                                                  value={v ?? ''} data-testid="field-{fl.label}"
                                                                  on:change={(e) => onInput(it.key, fl, e)}></textarea>
                                                    {:else if fl.kind === 'list'}
                                                        <input type="text" class="{inputCls} flex-1 font-mono"
                                                               placeholder={$t('sessionFeatures.listPlaceholder')}
                                                               value={listText(v)} data-testid="field-{fl.label}"
                                                               on:change={(e) => onInput(it.key, fl, e)} />
                                                    {:else}
                                                        <input type="text" class="{inputCls} w-56 font-mono"
                                                               value={v ?? ''} data-testid="field-{fl.label}"
                                                               on:change={(e) => onInput(it.key, fl, e)} />
                                                    {/if}
                                                {/if}
                                            </label>
                                        {/each}
                                        {#if it.fields.length === 0}
                                            <span class="text-text-muted opacity-70">{$t('sessionFeatures.editInSettings')}</span>
                                        {/if}
                                        <span class="ml-auto text-text-muted opacity-70" style="font-size:11px"
                                              title={$t(`sessionFeatures.scopeHint.${it.scope}`)}>
                                            {$t(`sessionFeatures.scope.${it.scope}`)} ·
                                            {LIVE.has(it.key) ? $t('sessionFeatures.appliesNow') : $t('sessionFeatures.appliesOnStart')}
                                        </span>
                                    </div>
                                </li>
                            {/each}
                        </ul>
                    </section>
                {/each}
                <p class="text-text-muted" style="font-size:11px">{$t('sessionFeatures.footer')}</p>
            {/if}
        </div>

        <!-- Status bar -->
        <div class="flex items-center gap-3 px-5 py-2.5 border-t border-bg-border text-xs">
            {#if error}
                <span class="text-status-error" data-testid="features-error">{error}</span>
            {:else if needsRestart}
                <span class="text-status-ratelimit" data-testid="features-needs-restart">{$t('sessionFeatures.needsRestart')}</span>
                <button
                    type="button"
                    class="ml-auto px-3 py-1 rounded bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40"
                    disabled={restarting}
                    data-testid="features-restart"
                    on:click={restartNow}>
                    {restarting ? '…' : $t('sessionFeatures.restartNow')}
                </button>
            {:else if saving}
                <span class="text-text-muted">{$t('sessionFeatures.saving')}</span>
            {:else}
                <span class="text-text-muted">{$t('sessionFeatures.autoSaveHint')}</span>
            {/if}
        </div>
    </div>
</div>

{#if helpItem}
    <!-- Help window over the popup; a click outside or Escape closes only it. -->
    <div
        class="fixed inset-0 z-[60] flex items-center justify-center bg-black/40"
        on:click={() => (helpItem = null)}
        on:keydown={(e) => e.key === 'Escape' && (helpItem = null)}
        role="dialog"
        aria-modal="true"
        tabindex="-1"
        data-testid="feature-help-dialog">
        <div
            class="bg-bg-panel border border-bg-border rounded-lg shadow-2xl w-[620px] max-w-[92vw] max-h-[80vh] flex flex-col"
            role="document"
            on:click|stopPropagation
            on:keydown|stopPropagation>
            <div class="flex items-start justify-between gap-3 px-5 pt-4 pb-3 border-b border-bg-border">
                <div class="min-w-0">
                    <div class="text-text font-semibold">{$t(`sessionFeatures.${helpItem.key}.title`)}</div>
                    <div class="text-text-muted font-mono mt-0.5" style="font-size:12px">
                        {helpItem.tomlKey} · {$t(`sessionFeatures.scope.${helpItem.scope}`)}
                    </div>
                </div>
                <button class="text-text-muted hover:text-text" type="button"
                        aria-label={$t('featureHelp.close')}
                        on:click={() => (helpItem = null)}>✕</button>
            </div>
            <div class="overflow-y-auto px-5 py-4 flex flex-col gap-3 text-sm text-text" data-testid="feature-help-body">
                {#each helpParas as para}
                    <p class="leading-relaxed">
                        {#each codeParts(para) as part}{#if part.code}<code class="font-mono px-1 rounded bg-bg-elevated" style="font-size:12px">{part.text}</code>{:else}{part.text}{/if}{/each}
                    </p>
                {/each}
                {#if helpExample}
                    <div class="border border-bg-border rounded bg-bg-elevated px-3 py-2" data-testid="feature-help-example">
                        <div class="text-text-muted uppercase tracking-wide mb-1" style="font-size:11px">{$t('featureHelp.example')}</div>
                        <p class="leading-relaxed">
                            {#each codeParts(helpExample) as part}{#if part.code}<code class="font-mono px-1 rounded bg-bg" style="font-size:12px">{part.text}</code>{:else}{part.text}{/if}{/each}
                        </p>
                    </div>
                {/if}
            </div>
        </div>
    </div>
{/if}
