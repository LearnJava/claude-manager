// Groups a flat LogEntry[] into a feed of blocks, the way Hermes Desktop
// shows agent progress: a run of tool calls collapses into one line, prose
// and user turns stand on their own. See VIEW-TASKS.md UI-02.
//
// Pure module on purpose (no Svelte, no i18n): stores/LogStream.svelte owns
// rendering and localized strings, this module only decides grouping and
// hands back structured summaries a component can format.

import type { LogEntry } from '../stores/sessions';
import { READ_TOOLS } from './formatters';

export type LogBlockKind = 'user' | 'prose' | 'thinking' | 'other' | 'tools';

// Everything a collapsed 'tools' block header needs, computed once so the
// component doesn't re-scan entries on every render.
export interface ToolsBlockSummary {
    // First tool call's display label (tool_input if present, else tool_name),
    // truncated to ~80 chars — the "первая команда или путь" from Hermes.
    firstLabel: string;
    // Additional tool calls beyond the first, for "+ N команд" / "+ N commands".
    extraCount: number;
    // True when every call in the group is a READ_TOOLS member — the group
    // collapses to "Просмотрено: a.go, b.go" instead of "cmd + N commands".
    readOnly: boolean;
    // Display labels for every call, in order — used for the read-only
    // summary's file list and available to the non-read-only path too.
    callLabels: string[];
    // An error (level 'error') anywhere in the group — must surface on the
    // collapsed header, never hidden by collapsing.
    hasError: boolean;
}

export interface LogBlock {
    kind: LogBlockKind;
    // seq of the group's first entry — stable across ring-buffer eviction and
    // search filtering, same reasoning as LogStream's per-row `overrides` map.
    seq: number;
    entries: LogEntry[];
    // Only present when kind === 'tools'.
    summary?: ToolsBlockSummary;
}

const MAX_LABEL_CHARS = 80;

// Fallback key for entries without a seq (mirrors LogStream.svelte's
// entryKey — negative range avoids colliding with real seq values).
export function entryKey(e: LogEntry, i: number): number {
    return e.seq ?? -(i + 1);
}

function truncate(s: string): string {
    const oneLine = s.split('\n', 1)[0];
    return oneLine.length > MAX_LABEL_CHARS ? oneLine.slice(0, MAX_LABEL_CHARS) + '…' : oneLine;
}

function callLabel(e: LogEntry): string {
    const input = (e.tool_input ?? '').trim();
    if (input) return truncate(input);
    return e.tool_name ?? '';
}

function buildToolsSummary(entries: LogEntry[]): ToolsBlockSummary {
    const calls = entries.filter((e) => (e.level ?? '').toLowerCase() === 'tool');
    const callLabels = calls.map(callLabel);
    const readOnly = calls.length > 0 && calls.every((e) => READ_TOOLS.has(e.tool_name ?? ''));
    const hasError = entries.some((e) => (e.level ?? '').toLowerCase() === 'error');
    return {
        firstLabel: callLabels[0] ?? '',
        extraCount: Math.max(0, callLabels.length - 1),
        readOnly,
        callLabels,
        hasError,
    };
}

// Levels that, once a tools group is open, extend it instead of closing it —
// "Внутри серии записи system и thinking не рвут группу" (VIEW-TASKS.md UI-02).
const TOOLS_PASSTHROUGH = new Set(['system', 'thinking']);

export function groupEntries(entries: LogEntry[]): LogBlock[] {
    const blocks: LogBlock[] = [];
    let cur: LogEntry[] | null = null;
    let curSeq = 0;

    const flush = () => {
        if (cur && cur.length > 0) {
            blocks.push({ kind: 'tools', seq: curSeq, entries: cur, summary: buildToolsSummary(cur) });
        }
        cur = null;
    };

    entries.forEach((e, i) => {
        const level = (e.level ?? '').toLowerCase();
        const key = entryKey(e, i);

        const isToolMember =
            level === 'tool' ||
            level === 'tool_result' ||
            // An error paired to a call (by tool_use_id) or simply adjacent to
            // an already-open series — "по соседству" per UI-02.
            (level === 'error' && (cur !== null || !!e.tool_use_id));

        if (isToolMember) {
            if (!cur) {
                cur = [];
                curSeq = key;
            }
            cur.push(e);
            return;
        }

        if (TOOLS_PASSTHROUGH.has(level) && cur) {
            // Doesn't break the open series — absorbed into it, still
            // rendered in its row when the group expands.
            cur.push(e);
            return;
        }

        // Anything else closes the open series (text and user rip it, per spec).
        flush();

        if (level === 'user') {
            blocks.push({ kind: 'user', seq: key, entries: [e] });
        } else if (level === 'text' || level === 'result') {
            blocks.push({ kind: 'prose', seq: key, entries: [e] });
        } else if (level === 'thinking') {
            blocks.push({ kind: 'thinking', seq: key, entries: [e] });
        } else {
            blocks.push({ kind: 'other', seq: key, entries: [e] });
        }
    });

    flush();
    return blocks;
}
