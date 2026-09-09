<script lang="ts">
    // Shown when ▶ is clicked on a session that has a crash-recovery state file
    // with a CLI session id — i.e. the previous run was interrupted (app killed,
    // session stopped mid-task, crash) and never reported a finished task.
    // Without this the manager silently resumed, so "continue" vs "start over"
    // was never the user's call; see CLAUDE.md "Crash Recovery".
    import { createEventDispatcher } from 'svelte';
    import { t } from '../lib/i18n';

    export let project: string;
    export let sessionName: string;
    /** PersistedState from GetSessionState (session_id / started_at / task). */
    export let state: { session_id: string; started_at?: any; task?: string };

    const dispatch = createEventDispatcher<{
        continue: void;
        fresh: void;
        cancel: void;
    }>();

    function startedAtLabel(v: any): string {
        if (!v) return '';
        const d = new Date(v);
        const ts = d.getTime();
        if (!ts || isNaN(ts)) return '';
        const abs = d.toLocaleString();
        const ms = Date.now() - ts;
        if (ms < 0) return abs;
        const m = Math.floor(ms / 60000);
        if (m < 1) return $t('resumePrompt.startedJustNow', { abs });
        if (m < 60) return $t('resumePrompt.startedMinutesAgo', { abs, m });
        const h = Math.floor(m / 60);
        if (h < 24) return $t('resumePrompt.startedHoursAgo', { abs, h, m: m % 60 });
        return $t('resumePrompt.startedDaysAgo', { abs, d: Math.floor(h / 24) });
    }
</script>

<!-- Backdrop -->
<div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    on:click={() => dispatch('cancel')}
    on:keydown={(e) => e.key === 'Escape' && dispatch('cancel')}
    role="dialog"
    aria-modal="true"
    tabindex="-1">

    <div
        class="bg-bg-panel border border-bg-border rounded-lg shadow-2xl w-[460px] p-5 flex flex-col gap-4"
        role="document"
        on:click|stopPropagation
        on:keydown|stopPropagation>

        <!-- Header -->
        <div class="flex items-center justify-between">
            <div>
                <span class="text-text font-semibold">{$t('resumePrompt.title')}</span>
                <span class="text-text-muted ml-2" style="font-size:13px">{project}/{sessionName}</span>
            </div>
            <button
                class="text-text-muted hover:text-text"
                title={$t('common.cancel')}
                on:click={() => dispatch('cancel')}
                type="button">✕</button>
        </div>

        <div class="text-text-muted" style="font-size:13px">
            {$t('resumePrompt.description')}
        </div>

        <div class="bg-bg-elevated border border-bg-border rounded p-3 space-y-1" style="font-size:13px">
            {#if state.task}
                <div class="flex gap-2">
                    <span class="text-text-muted shrink-0">{$t('resumePrompt.task')}</span>
                    <span class="text-text break-words" data-testid="resume-task">{state.task}</span>
                </div>
            {/if}
            {#if startedAtLabel(state.started_at)}
                <div class="flex gap-2">
                    <span class="text-text-muted shrink-0">{$t('resumePrompt.started')}</span>
                    <span class="text-text">{startedAtLabel(state.started_at)}</span>
                </div>
            {/if}
            <div class="flex gap-2">
                <span class="text-text-muted shrink-0">{$t('resumePrompt.cliSession')}</span>
                <span class="text-text font-mono break-all" style="font-size:11px">{state.session_id}</span>
            </div>
        </div>

        <!-- Footer -->
        <div class="flex justify-end gap-2 pt-1">
            <button
                type="button"
                on:click={() => dispatch('cancel')}
                class="px-3 py-1 rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg"
                style="font-size:13px">
                {$t('common.cancel')}
            </button>
            <button
                type="button"
                title={$t('resumePrompt.startFreshTooltip')}
                on:click={() => dispatch('fresh')}
                class="px-3 py-1 rounded bg-bg-elevated border border-bg-border text-text hover:bg-bg"
                style="font-size:13px">
                {$t('resumePrompt.startFresh')}
            </button>
            <button
                type="button"
                title={$t('resumePrompt.continueTooltip')}
                on:click={() => dispatch('continue')}
                class="px-4 py-1 rounded bg-blue-600 hover:bg-blue-500 text-white font-medium"
                style="font-size:13px">
                {$t('resumePrompt.continue')}
            </button>
        </div>
    </div>
</div>
