<script lang="ts">
    import { liveNow, formatLiveElapsed } from '../lib/liveStatus';

    // Ticking "· 4s" since an ISO timestamp; mounted only while something is live.
    export let since: string;

    $: ms = Date.parse(since);
    $: text = isNaN(ms) ? '' : formatLiveElapsed(Math.max(0, $liveNow - ms));
</script>

{#if text}<span data-testid="live-elapsed" class="shrink-0">· {text}</span>{/if}
