// Groups a flat LogEntry[] into a feed of blocks, the way Hermes Desktop
// shows agent progress: a run of tool calls collapses into one line, prose
// and user turns stand on their own. See VIEW-TASKS.md UI-02.
//
// Pure module on purpose (no Svelte, no i18n): stores/LogStream.svelte owns
// rendering and localized strings, this module only decides grouping and
// hands back structured summaries a component can format.

import type { DiffLine, LogEntry } from '../stores/sessions';
import { READ_TOOLS } from './formatters';

export type LogBlockKind = 'user' | 'prose' | 'thinking' | 'other' | 'tools' | 'edit';

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
    // Calls paired with their results (by tool_use_id, else by adjacency).
    toolCalls: ToolCall[];
    // An error (level 'error') anywhere in the group — must surface on the
    // collapsed header, never hidden by collapsing.
    hasError: boolean;
}

// Everything a collapsed 'edit' block's card needs — VIEW-TASKS.md UI-04's
// "карточка с именем и счётчиком строк" (file name + +N/-M) plus the diff
// itself for the expanded view.
export interface EditBlockSummary {
    // Full path as given by the tool's abbreviated input (tool_input) — shown
    // in the card's title attribute.
    filePath: string;
    // Last path segment, e.g. "store_test.go" — the card's visible label.
    fileName: string;
    added: number;
    removed: number;
    lines: DiffLine[];
    // Further lines beyond config.DiffLineLimit that were dropped, 0 if none.
    truncated: number;
}

// One tool call paired with its result — VIEW-TASKS.md UI-08. `running` is a
// call with no result yet; once the turn's `result` entry has arrived after it
// the call is closed as `ok` without a duration, so an interrupted turn never
// spins forever.
export interface ToolCall {
    call: LogEntry;
    result?: LogEntry;
    durationMs?: number;
    state: 'running' | 'ok' | 'error';
}

export interface LogBlock {
    kind: LogBlockKind;
    // seq of the group's first entry — stable across ring-buffer eviction and
    // search filtering, same reasoning as LogStream's per-row `overrides` map.
    seq: number;
    entries: LogEntry[];
    // Only present when kind === 'tools'.
    summary?: ToolsBlockSummary;
    // Only present when kind === 'edit'.
    editSummary?: EditBlockSummary;
    // Only present when kind === 'thinking'. Seconds between this entry's
    // `time` and the next entry in the session (not the next block) — the
    // agent kept thinking until something else happened. `null` means there
    // is no next entry yet, i.e. the agent is thinking right now (VIEW-TASKS.md
    // UI-03 "Думает…" without a duration).
    thinkingSeconds?: number | null;
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

// Last path segment of a file-edit tool's input, e.g. "/src/a/store_test.go"
// -> "store_test.go". Falls back to the full (truncated) label when there is
// no path separator (an unusual input, or a non-path abbreviation).
export function fileBaseName(path: string): string {
    const trimmed = path.trim();
    const parts = trimmed.split(/[\\/]/).filter(Boolean);
    return parts.length > 0 ? parts[parts.length - 1] : truncate(trimmed);
}

// Pairs each call in a group with its result. `turnEnded(call)` says whether a
// turn-level result entry followed the call, closing unanswered calls as ok.
export function pairToolCalls(entries: LogEntry[], turnEnded: (call: LogEntry) => boolean): ToolCall[] {
    const calls: ToolCall[] = [];
    const byId = new Map<string, ToolCall>();
    for (const e of entries) {
        const level = (e.level ?? '').toLowerCase();
        if (level === 'tool') {
            const tc: ToolCall = { call: e, state: 'running' };
            calls.push(tc);
            if (e.tool_use_id) byId.set(e.tool_use_id, tc);
        } else if (level === 'tool_result' || level === 'error') {
            let tc: ToolCall | undefined;
            if (e.tool_use_id) {
                tc = byId.get(e.tool_use_id);
            } else {
                // No id: the nearest preceding open call that has none either.
                for (let i = calls.length - 1; i >= 0; i--) {
                    if (!calls[i].result && !calls[i].call.tool_use_id) {
                        tc = calls[i];
                        break;
                    }
                }
            }
            if (!tc || tc.result) continue;
            tc.result = e;
            tc.state = level === 'error' ? 'error' : 'ok';
            if (e.duration_ms) tc.durationMs = e.duration_ms;
        }
    }
    for (const tc of calls) {
        if (tc.state === 'running' && turnEnded(tc.call)) tc.state = 'ok';
    }
    return calls;
}

function buildToolsSummary(entries: LogEntry[], turnEnded: (call: LogEntry) => boolean): ToolsBlockSummary {
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
        toolCalls: pairToolCalls(entries, turnEnded),
    };
}

function buildEditSummary(callEntry: LogEntry): EditBlockSummary {
    const filePath = (callEntry.tool_input ?? callEntry.tool_name ?? '').trim();
    const diff = callEntry.diff;
    return {
        filePath,
        fileName: fileBaseName(filePath),
        added: diff?.added ?? 0,
        removed: diff?.removed ?? 0,
        lines: diff?.lines ?? [],
        truncated: diff?.truncated ?? 0,
    };
}

// Levels that, once a tools/edit group is open, extend it instead of closing
// it — "Внутри серии записи system и thinking не рвут группу" (VIEW-TASKS.md
// UI-02).
const TOOLS_PASSTHROUGH = new Set(['system', 'thinking']);

// Seconds between two entries' `time` fields, or null if either is missing/
// invalid — the caller treats null as "still thinking" (no next entry yet).
function secondsBetween(from: LogEntry, to: LogEntry | undefined): number | null {
    if (!to) return null;
    const a = Date.parse(from.time);
    const b = Date.parse(to.time);
    if (isNaN(a) || isNaN(b)) return null;
    return Math.max(0, Math.round((b - a) / 1000));
}

export function groupEntries(entries: LogEntry[]): LogBlock[] {
    const blocks: LogBlock[] = [];
    // Index of the last turn-level `result` entry: a call before it that never
    // got its own result is closed, not running.
    let lastResultIdx = -1;
    const indexOf = new Map<LogEntry, number>();
    entries.forEach((e, i) => {
        indexOf.set(e, i);
        if ((e.level ?? '').toLowerCase() === 'result') lastResultIdx = i;
    });
    const turnEnded = (call: LogEntry) => (indexOf.get(call) ?? 0) < lastResultIdx;
    let cur: LogEntry[] | null = null;
    // Which kind the currently-open run will flush as. Only meaningful while
    // `cur` is non-null.
    let curKind: 'tools' | 'edit' = 'tools';
    let curSeq = 0;

    const flush = () => {
        if (cur && cur.length > 0) {
            if (curKind === 'edit') {
                blocks.push({ kind: 'edit', seq: curSeq, entries: cur, editSummary: buildEditSummary(cur[0]) });
            } else {
                blocks.push({ kind: 'tools', seq: curSeq, entries: cur, summary: buildToolsSummary(cur, turnEnded) });
            }
        }
        cur = null;
    };

    entries.forEach((e, i) => {
        const level = (e.level ?? '').toLowerCase();
        const key = entryKey(e, i);

        const isCall = level === 'tool';
        // A file-editing call (Edit/MultiEdit/Write, patch/write_file) gets
        // its own card, never merged into a run of other tool calls — VIEW-
        // TASKS.md UI-04 "отдельная карточка, а не строка внутри группы tools".
        const isEditCall = isCall && !!e.diff;

        const isResultMember =
            level === 'tool_result' ||
            // An error paired to a call (by tool_use_id) or simply adjacent to
            // an already-open series — "по соседству" per UI-02.
            (level === 'error' && (cur !== null || !!e.tool_use_id));

        if (isEditCall) {
            // Always starts a fresh block: closes whatever run (tools or a
            // previous edit) was open.
            flush();
            cur = [e];
            curKind = 'edit';
            curSeq = key;
            return;
        }

        if (isCall) {
            // A plain tool call closes an open edit card (only its own
            // result may follow it) before joining/starting a tools run.
            if (cur && curKind === 'edit') flush();
            if (!cur) {
                cur = [];
                curKind = 'tools';
                curSeq = key;
            }
            cur.push(e);
            return;
        }

        if (isResultMember) {
            // Attaches to whatever run is currently open — a tools series or
            // the single call of an open edit card.
            if (!cur) {
                cur = [];
                curKind = 'tools';
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
            blocks.push({
                kind: 'thinking',
                seq: key,
                entries: [e],
                thinkingSeconds: secondsBetween(e, entries[i + 1]),
            });
        } else {
            blocks.push({ kind: 'other', seq: key, entries: [e] });
        }
    });

    flush();
    return blocks;
}
