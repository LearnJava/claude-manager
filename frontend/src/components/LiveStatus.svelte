<script lang="ts">
    import { locale } from '../lib/i18n';
    import { liveNow, liveStatusLine, spinnerFrame, type LiveActivity } from '../lib/liveStatus';
    import type { ToolDisplayInput } from '../lib/toolDisplay';
    import { t } from '../lib/i18n';
    import { InterruptSession } from '../../wailsjs/go/main/App';

    export let activity: LiveActivity | undefined;
    export let openCall: ToolDisplayInput | null = null;
    // Set when the action can be interrupted (see lib/interrupt.ts): shows a
    // stop button that aborts the turn but keeps the session open.
    export let interruptId: string | null = null;

    let interrupting = false;
    let interruptError = '';

    async function onInterrupt() {
        if (!interruptId || interrupting) return;
        interrupting = true;
        interruptError = '';
        try {
            await InterruptSession(interruptId);
        } catch (e: any) {
            interruptError = e?.message ?? String(e);
        } finally {
            interrupting = false;
        }
    }

    $: line = liveStatusLine(activity, $liveNow, $locale, openCall);
</script>

{#if line}
    <div
        data-testid="live-status"
        data-kind={activity?.kind}
        class="sticky bottom-0 py-1 bg-bg text-text-dim italic flex items-center gap-2">
        <span class="shrink-0 select-none w-4 text-center">{spinnerFrame($liveNow)}</span>
        <span class="truncate">{line.text}</span>
        {#if line.elapsed}<span class="shrink-0">· {line.elapsed}</span>{/if}
        {#if interruptId}
            <button
                type="button"
                data-testid="live-interrupt"
                class="shrink-0 not-italic px-1.5 py-0.5 text-xs rounded border border-bg-border
                       bg-bg-elevated text-text hover:bg-red-500/20 hover:border-red-500/50
                       disabled:opacity-40 disabled:cursor-not-allowed"
                disabled={interrupting}
                on:click={onInterrupt}
                title={interruptError || $t('logStream.interrupt.title')}>
                {interrupting ? '…' : '■'} {$t('logStream.interrupt.label')}
            </button>
        {/if}
    </div>
{/if}
