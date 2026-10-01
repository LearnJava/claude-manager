<script lang="ts">
    import type { SessionState } from '../stores/sessions';
    import RoadmapTree from './RoadmapTree.svelte';
    import QueuePanel from './QueuePanel.svelte';
    import { queues } from '../stores/queues';
    import { EMPTY_QUEUE, hasQueue, queuedCount } from '../lib/queue';
    import { t } from '../lib/i18n';

    export let session: SessionState;

    // Two tabs: "Queue" is the slice of the backlog this session's status
    // file points at (only offered when there is one, then the default);
    // "Roadmap" is the project's whole backlog.
    type Tab = 'queue' | 'roadmap';
    type TabDef = { id: Tab; label: string };
    $: model = $queues[session.id]?.model ?? EMPTY_QUEUE;
    $: queueVisible = hasQueue(model);
    $: tabs = [
        ...(queueVisible
            ? [{ id: 'queue', label: `${$t('queue.tab')} · ${queuedCount(model)}` }]
            : []),
        { id: 'roadmap', label: $t('taskPanel.tabs.roadmap') },
    ] as TabDef[];
    // The tab the user picked for the session they are looking at; null means
    // "use the default", which keeps following the queue as it loads.
    let chosen: Tab | null = null;
    let tabForSession = '';
    $: if (session.id !== tabForSession) {
        tabForSession = session.id;
        chosen = null;
    }
    $: defaultTab = (queueVisible ? 'queue' : 'roadmap') as Tab;
    $: tab = chosen && tabs.some((x) => x.id === chosen) ? chosen : defaultTab;
</script>

<div class="w-72 shrink-0 border-l border-bg-border bg-bg-panel flex flex-col min-h-0">
    <div class="px-3 py-2 border-b border-bg-border flex items-center gap-1">
        {#each tabs as t}
            <button
                type="button"
                on:click={() => (chosen = t.id)}
                class="px-1.5 py-0.5 text-xs uppercase tracking-wide rounded
                       {tab === t.id
                    ? 'text-text font-semibold bg-bg-elevated'
                    : 'text-text-muted hover:text-text'}">
                {t.label}
            </button>
        {/each}
    </div>

    <div class="flex-1 min-h-0 overflow-y-auto px-3 py-2">
        {#if tab === 'queue'}
            <QueuePanel {session} />
        {:else}
            <RoadmapTree {session} />
        {/if}
    </div>
</div>
