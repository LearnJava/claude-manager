/**
 * Unit spec for src/lib/logGroups.ts — pure function, no DOM needed (same
 * style as formatters.spec.ts). See VIEW-TASKS.md UI-02 "Готово когда".
 */

import { test, expect } from '@playwright/test';
import { groupEntries, entryKey } from '../src/lib/logGroups';
import type { LogEntry } from '../src/stores/sessions';

let seq = 0;
function entry(partial: Partial<LogEntry>): LogEntry {
    seq += 1;
    return {
        time: new Date(2026, 0, 1, 0, 0, seq).toISOString(),
        level: '',
        source: 'claude',
        message: '',
        seq,
        ...partial,
    };
}

test.beforeEach(() => {
    seq = 0;
});

test.describe('groupEntries', () => {
    test('a run of 4 tool calls with results collapses into one tools block with +3', () => {
        const pairs: LogEntry[] = [];
        for (let i = 0; i < 4; i++) {
            const call = entry({ level: 'tool', tool_name: 'Bash', tool_input: `echo ${i}` });
            (call as any).tool_use_id = `c${i}`;
            const result = entry({ level: 'tool_result', message: `out ${i}` });
            (result as any).tool_use_id = `c${i}`;
            pairs.push(call, result);
        }

        const blocks = groupEntries(pairs);
        expect(blocks).toHaveLength(1);
        expect(blocks[0].kind).toBe('tools');
        expect(blocks[0].summary?.extraCount).toBe(3);
        expect(blocks[0].summary?.firstLabel).toBe('echo 0');
    });

    test('text between tool calls splits one series into two tools blocks', () => {
        const c1 = entry({ level: 'tool', tool_name: 'Bash', tool_input: 'echo 1' });
        (c1 as any).tool_use_id = 'c1';
        const r1 = entry({ level: 'tool_result', message: 'out 1' });
        (r1 as any).tool_use_id = 'c1';
        const textE = entry({ level: 'text', message: 'thinking out loud' });
        const c2 = entry({ level: 'tool', tool_name: 'Bash', tool_input: 'echo 2' });
        (c2 as any).tool_use_id = 'c2';
        const r2 = entry({ level: 'tool_result', message: 'out 2' });
        (r2 as any).tool_use_id = 'c2';

        const blocks = groupEntries([c1, r1, textE, c2, r2]);
        const toolsBlocks = blocks.filter((b) => b.kind === 'tools');
        expect(toolsBlocks).toHaveLength(2);
        expect(blocks.find((b) => b.kind === 'prose')).toBeTruthy();
    });

    test('a result without tool_use_id attaches to the preceding call', () => {
        const call = entry({ level: 'tool', tool_name: 'Bash', tool_input: 'echo hi' });
        const result = entry({ level: 'tool_result', message: 'hi' }); // no id — adjacency only

        const blocks = groupEntries([call, result]);
        expect(blocks).toHaveLength(1);
        expect(blocks[0].kind).toBe('tools');
        expect(blocks[0].entries).toHaveLength(2);
    });

    test('an error inside a tools series surfaces on the block summary', () => {
        const call = entry({ level: 'tool', tool_name: 'Bash', tool_input: 'false' });
        (call as any).tool_use_id = 'c1';
        const err = entry({ level: 'error', message: 'command failed' });
        (err as any).tool_use_id = 'c1';

        const blocks = groupEntries([call, err]);
        expect(blocks).toHaveLength(1);
        expect(blocks[0].summary?.hasError).toBe(true);
    });

    test('a read-only series (Read/Grep/Glob) is flagged readOnly', () => {
        const call1 = entry({ level: 'tool', tool_name: 'Read', tool_input: 'a.go' });
        const call2 = entry({ level: 'tool', tool_name: 'Grep', tool_input: 'foo' });

        const blocks = groupEntries([call1, call2]);
        expect(blocks).toHaveLength(1);
        expect(blocks[0].summary?.readOnly).toBe(true);
        expect(blocks[0].summary?.callLabels).toEqual(['a.go', 'foo']);
    });

    test('a series mixing a write tool with a read tool is not readOnly', () => {
        const call1 = entry({ level: 'tool', tool_name: 'Read', tool_input: 'a.go' });
        const call2 = entry({ level: 'tool', tool_name: 'Edit', tool_input: 'a.go' });

        const blocks = groupEntries([call1, call2]);
        expect(blocks[0].summary?.readOnly).toBe(false);
    });

    test('user, prose, thinking and other entries each get their own block', () => {
        const u = entry({ level: 'user', message: 'do the thing' });
        const p = entry({ level: 'text', message: 'ok, doing it' });
        const th = entry({ level: 'thinking', message: 'hmm' });
        const sys = entry({ level: 'system', message: 'connected' });

        const blocks = groupEntries([u, p, th, sys]);
        expect(blocks.map((b) => b.kind)).toEqual(['user', 'prose', 'thinking', 'other']);
    });

    test('system and thinking entries inside an open tools series do not break it', () => {
        const call1 = entry({ level: 'tool', tool_name: 'Bash', tool_input: 'echo 1' });
        const sys = entry({ level: 'system', message: 'note' });
        const th = entry({ level: 'thinking', message: 'hmm' });
        const call2 = entry({ level: 'tool', tool_name: 'Bash', tool_input: 'echo 2' });

        const blocks = groupEntries([call1, sys, th, call2]);
        expect(blocks).toHaveLength(1);
        expect(blocks[0].kind).toBe('tools');
        expect(blocks[0].entries).toHaveLength(4);
    });

    test('entryKey falls back to a negative index when seq is missing', () => {
        const e: LogEntry = { time: '', level: '', source: '', message: '' };
        expect(entryKey(e, 0)).toBe(-1);
        expect(entryKey(e, 3)).toBe(-4);
    });

    test('a thinking block reports the seconds until the next entry', () => {
        const th = entry({ level: 'thinking', message: 'hmm', time: '2026-01-01T00:00:00.000Z' });
        const next = entry({ level: 'text', message: 'ok', time: '2026-01-01T00:00:03.000Z' });

        const blocks = groupEntries([th, next]);
        const thinkingBlock = blocks.find((b) => b.kind === 'thinking');
        expect(thinkingBlock?.thinkingSeconds).toBe(3);
    });

    test('a thinking block with no following entry yet has no duration', () => {
        const th = entry({ level: 'thinking', message: 'hmm', time: '2026-01-01T00:00:00.000Z' });

        const blocks = groupEntries([th]);
        const thinkingBlock = blocks.find((b) => b.kind === 'thinking');
        expect(thinkingBlock?.thinkingSeconds).toBeNull();
    });

    test('a tool_use with a diff becomes its own edit block, not a tools block', () => {
        const call = entry({
            level: 'tool',
            tool_name: 'Edit',
            tool_input: '/src/store_test.go',
        });
        (call as any).diff = { added: 3, removed: 2, lines: [] };

        const blocks = groupEntries([call]);
        expect(blocks).toHaveLength(1);
        expect(blocks[0].kind).toBe('edit');
        expect(blocks[0].editSummary?.fileName).toBe('store_test.go');
        expect(blocks[0].editSummary?.added).toBe(3);
        expect(blocks[0].editSummary?.removed).toBe(2);
    });

    test('an edit block absorbs its own tool_result but not further calls', () => {
        const call = entry({ level: 'tool', tool_name: 'Write', tool_input: '/a.go' });
        (call as any).diff = { added: 10, removed: 0, lines: [] };
        (call as any).tool_use_id = 'e1';
        const result = entry({ level: 'tool_result', message: 'ok' });
        (result as any).tool_use_id = 'e1';
        const next = entry({ level: 'tool', tool_name: 'Bash', tool_input: 'go build' });

        const blocks = groupEntries([call, result, next]);
        expect(blocks.map((b) => b.kind)).toEqual(['edit', 'tools']);
        expect(blocks[0].entries).toHaveLength(2);
    });

    test('an edit block interrupts an open tools series', () => {
        const c1 = entry({ level: 'tool', tool_name: 'Bash', tool_input: 'ls' });
        const edit = entry({ level: 'tool', tool_name: 'Edit', tool_input: '/a.go' });
        (edit as any).diff = { added: 1, removed: 1, lines: [] };
        const c2 = entry({ level: 'tool', tool_name: 'Bash', tool_input: 'pwd' });

        const blocks = groupEntries([c1, edit, c2]);
        expect(blocks.map((b) => b.kind)).toEqual(['tools', 'edit', 'tools']);
    });
});

test.describe('tool call pairs (UI-08)', () => {
    const call = (id: string | undefined, extra: Partial<LogEntry> = {}) =>
        entry({ level: 'tool', tool_name: 'Bash', tool_input: 'x', tool_use_id: id, ...extra } as any);
    const res = (id: string | undefined, extra: Partial<LogEntry> = {}) =>
        entry({ level: 'tool_result', tool_use_id: id, ...extra } as any);

    test('pairs by tool_use_id when results arrive out of order', () => {
        const [a, b] = [call('a'), call('b')];
        const blocks = groupEntries([a, b, res('b', { duration_ms: 20 }), res('a', { duration_ms: 900 })]);
        const tc = blocks[0].summary!.toolCalls;
        expect(tc.map((t) => [t.call.tool_use_id, t.durationMs, t.state])).toEqual([
            ['a', 900, 'ok'],
            ['b', 20, 'ok'],
        ]);
    });

    test('a call without a result is running', () => {
        const blocks = groupEntries([call('a'), res('a'), call('b')]);
        const tc = blocks[0].summary!.toolCalls;
        expect(tc[0].state).toBe('ok');
        expect(tc[1].state).toBe('running');
        expect(tc[1].result).toBeUndefined();
    });

    test('a turn result after an unanswered call closes it as ok without duration', () => {
        const blocks = groupEntries([call('a'), entry({ level: 'result', message: 'done' })]);
        const tc = blocks[0].summary!.toolCalls;
        expect(tc[0].state).toBe('ok');
        expect(tc[0].durationMs).toBeUndefined();
    });

    test('error result gives state error', () => {
        const blocks = groupEntries([call('a'), entry({ level: 'error', tool_use_id: 'a' } as any)]);
        expect(blocks[0].summary!.toolCalls[0].state).toBe('error');
    });

    test('calls and results without ids pair by adjacency', () => {
        const blocks = groupEntries([call(undefined), res(undefined, { duration_ms: 5 }), call(undefined)]);
        const tc = blocks[0].summary!.toolCalls;
        expect(tc[0].state).toBe('ok');
        expect(tc[0].durationMs).toBe(5);
        expect(tc[1].state).toBe('running');
    });
});
