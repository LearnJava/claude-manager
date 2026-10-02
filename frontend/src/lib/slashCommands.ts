// "/" autocomplete for the session message box (SessionInput.svelte).
// The catalog itself comes from the backend (ListSlashCommands) and is
// already limited to what the session's runtime can run.

export interface SlashCommand {
    name: string;
    description?: string;
    kind: 'skill' | 'command' | string;
}

/**
 * The partial command name being typed, or null when the popup should be
 * closed: the message must start with "/" and the caret must still be inside
 * that first token.
 */
export function slashQuery(value: string, caret: number): string | null {
    if (!value.startsWith('/')) return null;
    const head = value.slice(0, caret);
    if (/\s/.test(head)) return null;
    const end = value.search(/\s/);
    const token = end < 0 ? value : value.slice(0, end);
    if (caret > token.length) return null;
    return token.slice(1);
}

/**
 * Case-insensitive match on the name: prefix hits first (also after a
 * "plugin:" namespace), then substring hits; each group keeps the catalog's
 * order.
 */
export function filterSlashCommands(list: SlashCommand[], query: string): SlashCommand[] {
    const q = query.toLowerCase();
    if (!q) return list;
    const prefix: SlashCommand[] = [];
    const rest: SlashCommand[] = [];
    for (const c of list) {
        const n = c.name.toLowerCase();
        const local = n.slice(n.lastIndexOf(':') + 1);
        if (n.startsWith(q) || local.startsWith(q)) prefix.push(c);
        else if (n.includes(q)) rest.push(c);
    }
    return [...prefix, ...rest];
}

/** Replaces the leading "/token" with "/name " and keeps whatever follows it. */
export function applySlashCommand(value: string, name: string): string {
    const end = value.search(/\s/);
    const tail = end < 0 ? '' : value.slice(end).replace(/^[ \t]+/, '');
    return `/${name} ${tail}`;
}
