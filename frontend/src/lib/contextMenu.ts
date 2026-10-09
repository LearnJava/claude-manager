// Right-click menu logic for ContextMenu.svelte. The production WebView2
// build has the native context menu disabled (Wails' default), so without
// this there is no way to copy a link or paste with the mouse.
//
// Kept DOM-light and framework-free so the item computation can be unit
// tested without mounting the component.

export type MenuAction = 'cut' | 'copy' | 'paste' | 'selectAll' | 'openLink' | 'copyLink' | 'copyCode';

export interface MenuContext {
    // Text field under the cursor (editable input/textarea), if any.
    field: HTMLInputElement | HTMLTextAreaElement | null;
    // Field selection captured at right-click time (focus may move later).
    fieldSel: { start: number; end: number } | null;
    // Selected page text (outside a field) at right-click time.
    selectedText: string;
    // href of the link under the cursor.
    link: string;
    // Text of the <pre> code block under the cursor.
    code: string;
}

const TEXT_INPUT_TYPES = new Set(['', 'text', 'search', 'url', 'email', 'tel', 'number', 'password']);

function asTextField(el: Element | null): HTMLInputElement | HTMLTextAreaElement | null {
    if (!el) return null;
    if (el instanceof HTMLTextAreaElement) return el;
    if (el instanceof HTMLInputElement && TEXT_INPUT_TYPES.has(el.type)) return el;
    return null;
}

export function buildContext(target: EventTarget | null): MenuContext {
    const el = target instanceof Element ? target : null;
    const field = asTextField(el?.closest('input, textarea') ?? null);
    let fieldSel: MenuContext['fieldSel'] = null;
    if (field) {
        try {
            fieldSel = { start: field.selectionStart ?? 0, end: field.selectionEnd ?? 0 };
        } catch {
            // type=number/email throw on selectionStart.
            fieldSel = null;
        }
    }
    const anchor = el?.closest('a[href]') as HTMLAnchorElement | null;
    const pre = el?.closest('pre');
    return {
        field,
        fieldSel,
        selectedText: field ? '' : (window.getSelection()?.toString() ?? ''),
        link: anchor?.href ?? '',
        code: pre?.textContent ?? '',
    };
}

function fieldSelectedText(ctx: MenuContext): string {
    if (!ctx.field || !ctx.fieldSel) return '';
    return ctx.field.value.slice(ctx.fieldSel.start, ctx.fieldSel.end);
}

export function copyableText(ctx: MenuContext): string {
    return ctx.field ? fieldSelectedText(ctx) : ctx.selectedText;
}

// Items in display order; '-' is a separator. Empty list = no menu.
export function menuItems(ctx: MenuContext): Array<MenuAction | '-'> {
    const groups: MenuAction[][] = [];
    if (ctx.link) groups.push(['openLink', 'copyLink']);
    if (ctx.field) {
        const editable = !ctx.field.readOnly && !ctx.field.disabled;
        const hasSel = fieldSelectedText(ctx) !== '' && ctx.field.type !== 'password';
        const g: MenuAction[] = [];
        if (editable && hasSel) g.push('cut');
        if (hasSel) g.push('copy');
        if (editable) g.push('paste');
        groups.push(g);
        groups.push(['selectAll']);
    } else {
        const g: MenuAction[] = [];
        if (ctx.selectedText) g.push('copy');
        if (ctx.code) g.push('copyCode');
        groups.push(g);
    }
    const out: Array<MenuAction | '-'> = [];
    for (const g of groups) {
        if (g.length === 0) continue;
        if (out.length) out.push('-');
        out.push(...g);
    }
    return out;
}

type WailsRuntime = {
    ClipboardSetText?: (t: string) => Promise<boolean>;
    ClipboardGetText?: () => Promise<string>;
    BrowserOpenURL?: (u: string) => void;
};

function wails(): WailsRuntime | undefined {
    return (window as unknown as { runtime?: WailsRuntime }).runtime;
}

export async function writeClipboard(text: string): Promise<boolean> {
    try {
        await navigator.clipboard.writeText(text);
        return true;
    } catch {
        // WebView2 may refuse navigator.clipboard; the Wails runtime goes
        // through the OS clipboard directly.
        try {
            return (await wails()?.ClipboardSetText?.(text)) ?? false;
        } catch {
            return false;
        }
    }
}

export async function readClipboard(): Promise<string> {
    // Prefer the Wails runtime: navigator.clipboard.readText needs a
    // permission grant WebView2 does not give by default.
    const rt = wails();
    if (rt?.ClipboardGetText) {
        try {
            return await rt.ClipboardGetText();
        } catch {
            /* fall through */
        }
    }
    try {
        return await navigator.clipboard.readText();
    } catch {
        return '';
    }
}

export function openLink(url: string) {
    const rt = wails();
    if (rt?.BrowserOpenURL) rt.BrowserOpenURL(url);
    else window.open(url, '_blank', 'noopener');
}

// Replace the captured selection in a field with text, keeping Svelte's
// bind:value in sync (an `input` event fires) and the native undo stack.
export function insertIntoField(field: HTMLInputElement | HTMLTextAreaElement, sel: MenuContext['fieldSel'], text: string) {
    field.focus();
    if (sel) {
        try {
            field.setSelectionRange(sel.start, sel.end);
        } catch {
            /* type without selection API */
        }
    }
    // execCommand is deprecated but still the only way to get undo support.
    if (document.execCommand('insertText', false, text)) return;
    const start = sel?.start ?? field.value.length;
    const end = sel?.end ?? start;
    field.setRangeText(text, start, end, 'end');
    field.dispatchEvent(new Event('input', { bubbles: true }));
}
