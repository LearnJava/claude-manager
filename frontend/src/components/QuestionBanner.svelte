<script lang="ts">
    import { onMount, onDestroy } from 'svelte';
    import { sessions, type SessionState } from '../stores/sessions';
    import { AnswerQuestion } from '../../wailsjs/go/main/App';

    export let session: SessionState;

    // 1s ticker so the "waiting (Ns)" label updates live — the backend
    // auto-answers with the first option after 5 minutes if nobody responds
    // here, so it's worth showing how long that's been.
    let now = Date.now();
    let tick: ReturnType<typeof setInterval>;

    onMount(() => {
        tick = setInterval(() => (now = Date.now()), 1000);
    });
    onDestroy(() => {
        if (tick) clearInterval(tick);
    });

    let busy = false;
    let error = '';
    let freeText = '';

    $: q = session.pending_question ?? null;
    $: askedAtMs = q?.asked_at ? new Date(q.asked_at).getTime() : now;
    $: waitedSec = Math.max(0, Math.floor((now - (askedAtMs || now)) / 1000));

    function formatWait(sec: number): string {
        if (sec < 60) return `${sec}s`;
        const m = Math.floor(sec / 60);
        const s = sec % 60;
        return `${m}m ${s}s`;
    }

    async function answer(text: string) {
        if (busy || !q || !text.trim()) return;
        busy = true;
        error = '';
        try {
            await AnswerQuestion(session.id, q.id, text.trim());
            // Optimistically clear the banner — the backend doesn't emit a
            // "question cleared" event, only a subsequent status change.
            sessions.update((map) => {
                const cur = map[session.id];
                if (!cur) return map;
                return { ...map, [session.id]: { ...cur, pending_question: null } };
            });
            freeText = '';
        } catch (e: any) {
            error = `Answer failed: ${e?.message ?? String(e)}`;
        } finally {
            busy = false;
        }
    }
</script>

{#if q}
    <div
        class="question-banner border-l-4 border-status-waiting
               bg-bg-elevated border-t border-r border-b border-bg-border
               px-3 py-2 m-2 rounded shadow"
        role="alertdialog"
        aria-live="assertive">
        <!-- Header: title + waiting time -->
        <div class="flex items-center gap-2 text-sm">
            <span class="text-status-waiting">❓</span>
            <span class="font-semibold text-text">
                {session.name} is asking a question
            </span>
            <span class="text-text-muted text-xs ml-auto tabular-nums" title="Time waited (auto-answers after 5m)">
                {formatWait(waitedSec)}
            </span>
        </div>

        <div class="mt-1.5 text-sm text-text break-words">{q.question}</div>

        {#if error}
            <div class="mt-1 text-xs text-status-error">{error}</div>
        {/if}

        <!-- Option buttons -->
        {#if q.options && q.options.length > 0}
            <div class="mt-2 flex flex-wrap gap-2">
                {#each q.options as opt}
                    <button
                        type="button"
                        on:click={() => answer(opt)}
                        disabled={busy}
                        class="px-2.5 py-1 text-xs rounded font-medium
                               bg-status-waiting/90 hover:bg-status-waiting text-white
                               disabled:opacity-50 disabled:cursor-not-allowed">
                        {opt}
                    </button>
                {/each}
            </div>
        {/if}

        <!-- Free-text answer -->
        <form class="mt-2 flex gap-2" on:submit|preventDefault={() => answer(freeText)}>
            <input
                type="text"
                bind:value={freeText}
                disabled={busy}
                placeholder="Or type a custom answer…"
                class="flex-1 px-2 py-1 text-xs rounded
                       bg-bg-panel border border-bg-border text-text
                       disabled:opacity-50" />
            <button
                type="submit"
                disabled={busy || !freeText.trim()}
                class="px-2.5 py-1 text-xs rounded
                       bg-bg-panel border border-bg-border text-text hover:bg-bg
                       disabled:opacity-50 disabled:cursor-not-allowed">
                {busy ? '…' : 'Send'}
            </button>
        </form>
    </div>
{/if}
