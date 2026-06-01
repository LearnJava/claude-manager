<script lang="ts">
    import type { SessionState } from '../stores/sessions';
    import { SendMessage } from '../../wailsjs/go/main/App';

    export let session: SessionState;

    let value = '';
    let sending = false;
    let error = '';
    let textarea: HTMLTextAreaElement | undefined;

    // A session can receive input while it's running. Allow input even when
    // working — the CLI queues it for the next turn.
    $: canSend =
        !!session &&
        session.status !== 'idle' &&
        session.status !== 'stopping' &&
        session.status !== 'error';

    async function onSend() {
        if (!canSend || sending) return;
        const msg = value.trim();
        if (!msg) return;
        sending = true;
        error = '';
        try {
            await SendMessage(session.id, msg);
            value = '';
        } catch (e: any) {
            error = e?.message ?? String(e);
        } finally {
            sending = false;
            textarea?.focus();
        }
    }

    function onKeydown(e: KeyboardEvent) {
        // Enter sends, Shift+Enter inserts a newline.
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            onSend();
        }
    }
</script>

<div class="bg-bg-panel border-t border-bg-border px-3 py-2">
    {#if error}
        <div class="text-status-error text-xs mb-1">Send failed: {error}</div>
    {/if}
    <div class="flex items-end gap-2">
        <textarea
            bind:this={textarea}
            bind:value
            on:keydown={onKeydown}
            disabled={!canSend || sending}
            rows="2"
            placeholder={canSend
                ? 'Type a message — Enter to send, Shift+Enter for newline'
                : 'Session is not active'}
            class="flex-1 resize-none bg-bg border border-bg-border rounded px-2 py-1
                   text-sm text-text placeholder:text-text-dim
                   focus:outline-none focus:border-status-starting
                   disabled:opacity-50 disabled:cursor-not-allowed"
        ></textarea>
        <button
            type="button"
            on:click={onSend}
            disabled={!canSend || sending || !value.trim()}
            class="bg-status-starting hover:bg-blue-500 text-white text-sm font-medium
                   px-3 py-1.5 rounded shrink-0
                   disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:bg-status-starting">
            {sending ? 'Sending…' : 'Send ▶'}
        </button>
    </div>
</div>
