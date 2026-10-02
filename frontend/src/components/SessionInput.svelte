<script lang="ts">
    import type { SessionState } from '../stores/sessions';
    import { t } from '../lib/i18n';
    import { tick } from 'svelte';
    import { ListSlashCommands, SendMessage, SendMessageWithImages } from '../../wailsjs/go/main/App';
    import {
        applySlashCommand,
        filterSlashCommands,
        slashQuery,
        type SlashCommand,
    } from '../lib/slashCommands';

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

    // ── "/" autocomplete ─────────────────────────────────────────────────────
    // The catalog is per runtime (Claude Code commands+skills, Hermes skills)
    // and is refetched every time the popup opens: a Claude session's list
    // becomes exact once its run reports the init line.
    const MAX_MATCHES = 50;
    let commands: SlashCommand[] = [];
    let commandsLoaded = false;
    let caret = 0;
    let menuIndex = 0;
    let menuDismissed = false;
    let prevQuery: string | null = null;
    let menuEl: HTMLUListElement | undefined;

    $: query = canSend ? slashQuery(value, caret) : null;
    $: {
        if (query !== null && prevQuery === null) loadCommands(session.id);
        if (query === null) menuDismissed = false;
        prevQuery = query;
    }
    $: matches = query === null ? [] : filterSlashCommands(commands, query).slice(0, MAX_MATCHES);
    $: menuOpen = query !== null && !menuDismissed && commandsLoaded;
    $: query, (menuIndex = 0);
    $: isHermes = session?.runtime === 'hermes';

    async function loadCommands(id: string) {
        commandsLoaded = false;
        try {
            const list = (await ListSlashCommands(id)) ?? [];
            if (session.id === id) commands = list;
        } catch {
            if (session.id === id) commands = [];
        }
        if (session.id === id) commandsLoaded = true;
    }

    function syncCaret() {
        caret = textarea?.selectionStart ?? value.length;
    }

    async function pickCommand(cmd: SlashCommand) {
        value = applySlashCommand(value, cmd.name);
        const pos = cmd.name.length + 2; // "/" + name + " "
        await tick();
        textarea?.focus();
        textarea?.setSelectionRange(pos, pos);
        caret = pos;
    }

    async function moveSelection(delta: number) {
        if (matches.length === 0) return;
        menuIndex = (menuIndex + delta + matches.length) % matches.length;
        await tick();
        menuEl?.querySelector<HTMLElement>(`[data-index="${menuIndex}"]`)?.scrollIntoView({ block: 'nearest' });
    }

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
        if (menuOpen) {
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                e.preventDefault();
                moveSelection(e.key === 'ArrowDown' ? 1 : -1);
                return;
            }
            if (((e.key === 'Enter' && !e.shiftKey) || e.key === 'Tab') && matches.length > 0) {
                e.preventDefault();
                pickCommand(matches[menuIndex]);
                return;
            }
            if (e.key === 'Escape') {
                e.preventDefault();
                menuDismissed = true;
                return;
            }
        }
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
        <div class="relative flex-1 flex">
        {#if menuOpen}
            <div
                class="absolute bottom-full left-0 right-0 mb-1 z-20 bg-bg-panel border border-bg-border
                       rounded shadow-lg text-sm"
                data-testid="slash-menu">
                <div class="flex justify-between gap-2 px-2 py-1 text-[11px] text-text-dim border-b border-bg-border">
                    <span>{isHermes ? $t('sessionInput.slash.header.hermes') : $t('sessionInput.slash.header.claude')}</span>
                    <span>{$t('sessionInput.slash.hint')}</span>
                </div>
                {#if matches.length === 0}
                    <div class="px-2 py-1.5 text-text-dim text-xs">{$t('sessionInput.slash.empty')}</div>
                {:else}
                    <ul bind:this={menuEl} class="max-h-64 overflow-y-auto py-0.5" role="listbox">
                        {#each matches as cmd, idx (cmd.name)}
                            <li
                                role="option"
                                aria-selected={idx === menuIndex}
                                data-index={idx}
                                data-testid="slash-item"
                                on:mousedown|preventDefault={() => pickCommand(cmd)}
                                on:mousemove={() => (menuIndex = idx)}
                                class="flex items-baseline gap-2 px-2 py-1 cursor-pointer
                                       {idx === menuIndex ? 'bg-status-starting/20' : ''}">
                                <span class="font-mono text-text shrink-0">/{cmd.name}</span>
                                <span
                                    class="text-[10px] uppercase tracking-wide shrink-0
                                           {cmd.kind === 'skill' ? 'text-purple-600 dark:text-purple-300' : 'text-text-dim'}">
                                    {cmd.kind === 'skill' ? $t('sessionInput.slash.skill') : $t('sessionInput.slash.command')}
                                </span>
                                {#if cmd.description}
                                    <span class="text-text-dim text-xs truncate" title={cmd.description}>{cmd.description}</span>
                                {/if}
                            </li>
                        {/each}
                    </ul>
                {/if}
            </div>
        {/if}
        <textarea
            bind:this={textarea}
            bind:value
            on:keydown={onKeydown}
            on:input={syncCaret}
            on:keyup={syncCaret}
            on:click={syncCaret}
            on:blur={() => (menuDismissed = true)}
            on:focus={() => (menuDismissed = false)}
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
        </div>
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
