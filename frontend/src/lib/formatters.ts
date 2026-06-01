// Display helpers for log entries, costs, tokens, durations.
// All formatters are pure functions; they tolerate undefined / null / NaN.

export function formatTime(input: string | number | Date | undefined | null): string {
    if (input === undefined || input === null || input === '') return '';
    const d = input instanceof Date ? input : new Date(input);
    const t = d.getTime();
    if (!t || isNaN(t)) return '';
    const hh = String(d.getHours()).padStart(2, '0');
    const mm = String(d.getMinutes()).padStart(2, '0');
    const ss = String(d.getSeconds()).padStart(2, '0');
    return `${hh}:${mm}:${ss}`;
}

export function formatCost(n: number | undefined | null): string {
    if (n === undefined || n === null || isNaN(n as number)) return '$0.00';
    const v = Number(n);
    if (v >= 100) return `$${v.toFixed(0)}`;
    if (v >= 10) return `$${v.toFixed(2)}`;
    if (v >= 1) return `$${v.toFixed(3)}`;
    return `$${v.toFixed(4)}`;
}

export function formatTokens(n: number | undefined | null): string {
    if (n === undefined || n === null || isNaN(n as number)) return '0';
    const v = Number(n);
    if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
    if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
    return String(Math.round(v));
}

export function formatDuration(ms: number | undefined | null): string {
    if (ms === undefined || ms === null || isNaN(ms as number)) return '0s';
    let s = Math.max(0, Math.floor(Number(ms) / 1000));
    const h = Math.floor(s / 3600);
    s -= h * 3600;
    const m = Math.floor(s / 60);
    s -= m * 60;
    if (h > 0) return `${h}h ${m}m`;
    if (m > 0) return `${m}m ${s}s`;
    return `${s}s`;
}

export function formatPercent(v: number | undefined | null): string {
    if (v === undefined || v === null || isNaN(v as number)) return '0%';
    return `${Math.round(Number(v) * 100)}%`;
}

// Cache hit ratio = reads / (reads + creation). Returns [0..1].
export function cacheHitRatio(read: number, creation: number): number {
    const r = Number(read) || 0;
    const c = Number(creation) || 0;
    const total = r + c;
    if (total <= 0) return 0;
    return r / total;
}

// Color class for the context-usage bar fill (Tailwind class names).
export function contextBarColor(util: number | undefined | null): string {
    const u = Number(util) || 0;
    if (u >= 0.8) return 'bg-status-error';
    if (u >= 0.6) return 'bg-status-ratelimit';
    return 'bg-status-working';
}

// ---- Log entry styling ----

// Tool families used to colour-code tool_use entries.
const READ_TOOLS = new Set(['Read', 'Grep', 'Glob', 'LS', 'NotebookRead']);
const BASH_TOOLS = new Set(['Bash', 'BashOutput', 'KillBash', 'KillShell']);
const EDIT_TOOLS = new Set(['Edit', 'Write', 'MultiEdit', 'NotebookEdit', 'ApplyDiff']);
const AGENT_TOOLS = new Set(['Task', 'Agent']);

export type LogEntryLike = {
    time?: string | Date;
    level?: string;
    source?: string;
    message?: string;
    tool_name?: string;
    tool_input?: string;
};

// Tailwind text-color class for a given log entry.
export function logEntryColor(e: LogEntryLike): string {
    const level = (e.level ?? '').toLowerCase();
    if (level === 'error') return 'text-status-error';
    if (level === 'result') return 'text-text-muted';
    if (level === 'system') return 'text-text-dim';
    if (level === 'thinking') return 'text-text-dim italic';
    if (level === 'cost') return 'text-status-ratelimit';
    if (level === 'tool') {
        const t = e.tool_name ?? '';
        if (READ_TOOLS.has(t)) return 'text-sky-400';
        if (BASH_TOOLS.has(t)) return 'text-status-working';
        if (EDIT_TOOLS.has(t)) return 'text-status-waiting';
        if (AGENT_TOOLS.has(t)) return 'text-purple-400';
        return 'text-sky-400';
    }
    // text (Claude reasoning) and unknown
    return 'text-text-muted';
}

export function logEntryIcon(e: LogEntryLike): string {
    const level = (e.level ?? '').toLowerCase();
    if (level === 'error') return '✖';
    if (level === 'result') return '✓';
    if (level === 'system') return 'ℹ';
    if (level === 'thinking') return '…';
    if (level === 'cost') return '📊';
    if (level === 'tool') {
        const t = e.tool_name ?? '';
        if (READ_TOOLS.has(t)) return '🔍';
        if (BASH_TOOLS.has(t)) return '$';
        if (EDIT_TOOLS.has(t)) return '✎';
        if (AGENT_TOOLS.has(t)) return '🤖';
        return '🔧';
    }
    return '💬';
}
