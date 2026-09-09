<script lang="ts">
    import type { SessionState } from '../stores/sessions';
    import { t } from '../lib/i18n';
    import { SendMessage, SendMessageWithImages } from '../../wailsjs/go/main/App';

    export let session: SessionState;

    type PendingImage = { mediaType: string; dataBase64: string; previewUrl: string };

    let value = '';
    let sending = false;
    let error = '';
    let textarea: HTMLTextAreaElement | undefined;
    let pendingImages: PendingImage[] = [];

    // A session can receive input while it's running. Allow input even when
    // working — the CLI queues it for the next turn.
    $: canSend =
        !!session &&
        session.status !== 'idle' &&
        session.status !== 'stopping' &&
        session.status !== 'error';

    function readAsDataURL(file: File): Promise<string> {
        return new Promise((resolve, reject) => {
            const reader = new FileReader();
            reader.onload = () => resolve(reader.result as string);
            reader.onerror = () =>
                reject(reader.error ?? new Error($t('sessionInput.pasteReadError')));
            reader.readAsDataURL(file);
        });
    }

    async function onPaste(e: ClipboardEvent) {
        const items = e.clipboardData?.items;
        if (!items) return;
        const imageItems = Array.from(items).filter((it) => it.type.startsWith('image/'));
        if (imageItems.length === 0) return;
        // Only intercept the paste when it actually carries image data — plain
        // text paste must keep working exactly as before.
        e.preventDefault();
        for (const item of imageItems) {
            const file = item.getAsFile();
            if (!file) continue;
            try {
                const dataUrl = await readAsDataURL(file);
                const match = dataUrl.match(/^data:(.+?);base64,(.*)$/);
                if (!match) continue;
                const [, mediaType, dataBase64] = match;
                pendingImages = [...pendingImages, { mediaType, dataBase64, previewUrl: dataUrl }];
            } catch (err: any) {
                error = err?.message ?? String(err);
            }
        }
    }

    function removeImage(idx: number) {
        pendingImages = pendingImages.filter((_, i) => i !== idx);
    }

    async function onSend() {
        if (!canSend || sending) return;
        const msg = value.trim();
        if (!msg && pendingImages.length === 0) return;
        sending = true;
        error = '';
        try {
            if (pendingImages.length > 0) {
                await SendMessageWithImages(
                    session.id,
                    msg,
                    pendingImages.map((img) => ({ media_type: img.mediaType, data_base64: img.dataBase64 }))
                );
                pendingImages = [];
            } else {
                await SendMessage(session.id, msg);
            }
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
        <div class="text-status-error text-xs mb-1">{$t('sessionInput.sendFailed')} {error}</div>
    {/if}
    {#if pendingImages.length > 0}
        <div class="flex flex-wrap gap-2 mb-2">
            {#each pendingImages as img, idx (img.previewUrl)}
                <div class="relative group">
                    <img
                        src={img.previewUrl}
                        alt={$t('sessionInput.pastedImageAlt')}
                        class="h-14 w-14 object-cover rounded border border-bg-border" />
                    <button
                        type="button"
                        on:click={() => removeImage(idx)}
                        title={$t('sessionInput.removeImage')}
                        class="absolute -top-1.5 -right-1.5 h-4 w-4 rounded-full bg-status-error
                               text-white text-[10px] leading-4 text-center
                               opacity-80 hover:opacity-100">
                        ✕
                    </button>
                </div>
            {/each}
        </div>
    {/if}
    <div class="flex items-end gap-2">
        <textarea
            bind:this={textarea}
            bind:value
            on:keydown={onKeydown}
            on:paste={onPaste}
            disabled={!canSend || sending}
            rows="2"
            placeholder={canSend
                ? $t('sessionInput.placeholder.active')
                : $t('sessionInput.placeholder.inactive')}
            class="flex-1 resize-none bg-bg border border-bg-border rounded px-2 py-1
                   text-sm text-text placeholder:text-text-dim
                   focus:outline-none focus:border-status-starting
                   disabled:opacity-50 disabled:cursor-not-allowed"
        ></textarea>
        <button
            type="button"
            on:click={onSend}
            disabled={!canSend || sending || (!value.trim() && pendingImages.length === 0)}
            class="bg-status-starting hover:bg-blue-500 text-white text-sm font-medium
                   px-3 py-1.5 rounded shrink-0
                   disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:bg-status-starting">
            {sending ? $t('sessionInput.sending') : $t('sessionInput.send')}
        </button>
    </div>
</div>
