<script lang="ts">
    import { locale } from '../lib/i18n';
    import { liveNow, liveStatusLine, spinnerFrame, type LiveActivity } from '../lib/liveStatus';
    import type { ToolDisplayInput } from '../lib/toolDisplay';

    export let activity: LiveActivity | undefined;
    export let openCall: ToolDisplayInput | null = null;

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
    </div>
{/if}
