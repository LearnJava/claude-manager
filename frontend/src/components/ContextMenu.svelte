<script lang="ts">
    // App-wide right-click menu: copy / cut / paste / select all in text
    // fields, copy for selected text, open / copy for links, copy for code
    // blocks. Mounted once in App.svelte; see lib/contextMenu.ts.
    import { tick } from 'svelte';
    import { t } from '../lib/i18n';
    import {
        buildContext,
        copyableText,
        insertIntoField,
        menuItems,
        openLink,
        readClipboard,
        writeClipboard,
        type MenuAction,
        type MenuContext,
    } from '../lib/contextMenu';

    let open = false;
    let x = 0;
    let y = 0;
    let items: Array<MenuAction | '-'> = [];
    let ctx: MenuContext | null = null;
    let menuEl: HTMLDivElement;

    const LABELS: Record<MenuAction, string> = {
        cut: 'contextMenu.cut',
        copy: 'contextMenu.copy',
        paste: 'contextMenu.paste',
        selectAll: 'contextMenu.selectAll',
        openLink: 'contextMenu.openLink',
        copyLink: 'contextMenu.copyLink',
        copyCode: 'contextMenu.copyCode',
    };

    async function onContextMenu(e: MouseEvent) {
        const c = buildContext(e.target);
        const list = menuItems(c);
        e.preventDefault();
        if (list.length === 0) {
            close();
            return;
        }
        ctx = c;
        items = list;
        x = e.clientX;
        y = e.clientY;
        open = true;
        await tick();
        // Keep the menu inside the viewport.
        if (menuEl) {
            const r = menuEl.getBoundingClientRect();
            if (x + r.width > window.innerWidth) x = Math.max(0, window.innerWidth - r.width - 4);
            if (y + r.height > window.innerHeight) y = Math.max(0, window.innerHeight - r.height - 4);
        }
    }

    function close() {
        open = false;
        ctx = null;
    }

    async function run(action: MenuAction) {
        const c = ctx;
        close();
        if (!c) return;
        switch (action) {
            case 'copy':
                await writeClipboard(copyableText(c));
                break;
            case 'cut':
                if (c.field) {
                    await writeClipboard(copyableText(c));
                    insertIntoField(c.field, c.fieldSel, '');
                }
                break;
            case 'paste':
                if (c.field) {
                    const text = await readClipboard();
                    if (text) insertIntoField(c.field, c.fieldSel, text);
                }
                break;
            case 'selectAll':
                c.field?.focus();
                c.field?.select();
                break;
            case 'openLink':
                openLink(c.link);
                break;
            case 'copyLink':
                await writeClipboard(c.link);
                break;
            case 'copyCode':
                await writeClipboard(c.code);
                break;
        }
    }

    function onWindowMouseDown(e: MouseEvent) {
        if (open && menuEl && !menuEl.contains(e.target as Node)) close();
    }

    function onKeyDown(e: KeyboardEvent) {
        // Capture phase: Escape closes the menu only — it must not also reach
        // the focused field (a search box clears itself on Escape).
        if (open && e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            close();
        }
    }
</script>

<svelte:window
    on:contextmenu={onContextMenu}
    on:mousedown|capture={onWindowMouseDown}
    on:keydown|capture={onKeyDown}
    on:blur={close}
    on:resize={close}
    on:wheel|passive={close} />

{#if open}
    <!-- mousedown is swallowed so the click does not steal focus/selection
         from the field the menu acts on. -->
    <div
        bind:this={menuEl}
        class="fixed z-[1000] min-w-[180px] py-1 rounded border border-bg-border bg-bg-panel shadow-lg text-sm text-text select-none"
        style="left: {x}px; top: {y}px;"
        role="menu"
        data-testid="context-menu"
        tabindex="-1"
        on:mousedown|preventDefault
        on:contextmenu|preventDefault|stopPropagation>
        {#each items as item}
            {#if item === '-'}
                <div class="my-1 border-t border-bg-border" role="separator"></div>
            {:else}
                <button
                    type="button"
                    role="menuitem"
                    data-action={item}
                    class="block w-full text-left px-3 py-1 hover:bg-bg-elevated"
                    on:click={() => run(item)}>{$t(LABELS[item])}</button>
            {/if}
        {/each}
    </div>
{/if}
