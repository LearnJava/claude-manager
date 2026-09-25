/**
 * Unit spec for src/lib/toolDisplay.ts — pure function, no DOM needed.
 * See VIEW-TASKS.md UI-10 "Готово когда".
 */

import { test, expect } from '@playwright/test';
import { toolDisplay, summarizeShellCommand, shortenPath } from '../src/lib/toolDisplay';
import { groupEntries } from '../src/lib/logGroups';
import type { LogEntry } from '../src/stores/sessions';

const td = (tool_name: string, tool_args?: Record<string, string>, tool_input?: string, lang: 'ru' | 'en' = 'en') =>
    toolDisplay({ tool_name, tool_args, tool_input }, lang);

test.describe('toolDisplay table', () => {
    for (const name of ['Read', 'read_file']) {
        test(`${name}: basename and line range`, () => {
            const d = td(name, { file_path: '/a/b/main.go', path: '/a/b/main.go', offset: '10', limit: '40' });
            expect(d).toMatchObject({ emoji: '📖', verb: 'read', detail: 'main.go L10-49' });
            expect(d.running).toBe('Reading main.go L10-49');
            expect(td(name, { path: '/a/main.go' }, undefined, 'ru').running).toBe('Читаю main.go');
        });
    }

    for (const name of ['Write', 'write_file']) {
        test(`${name}: path`, () => {
            expect(td(name, { path: 'src/a.go' })).toMatchObject({ emoji: '✍️', verb: 'write', detail: 'src/a.go' });
        });
    }

    for (const name of ['Edit', 'MultiEdit', 'patch']) {
        test(`${name}: path`, () => {
            expect(td(name, { file_path: 'src/a.go' })).toMatchObject({ emoji: '🔧', verb: 'edit', detail: 'src/a.go' });
        });
    }

    for (const name of ['Bash', 'terminal']) {
        test(`${name}: description wins, else compressed command`, () => {
            expect(td(name, { command: 'ls', description: 'List files' })).toMatchObject({
                emoji: '💻',
                verb: '$',
                detail: 'List files',
            });
            expect(td(name, { command: 'cd x && npm test | tail -5' }).detail).toBe('npm test');
        });
    }

    test('Grep / search_files: pattern with path', () => {
        expect(td('Grep', { pattern: 'foo', path: 'src' })).toMatchObject({ emoji: '🔎', verb: 'grep', detail: 'foo in src' });
        expect(td('search_files', { pattern: 'foo' })).toMatchObject({ verb: 'grep', detail: 'foo' });
    });

    test('Glob / search_files target=files: find', () => {
        expect(td('Glob', { pattern: '**/*.go' })).toMatchObject({ emoji: '🔎', verb: 'find', detail: '**/*.go' });
        expect(td('search_files', { pattern: '*.go', target: 'files' })).toMatchObject({ verb: 'find', detail: '*.go' });
    });

    for (const name of ['WebSearch', 'web_search']) {
        test(`${name}: query`, () => {
            expect(td(name, { query: 'wails events' })).toMatchObject({ emoji: '🔍', verb: 'search', detail: 'wails events' });
        });
    }

    for (const name of ['WebFetch', 'web_extract']) {
        test(`${name}: domain`, () => {
            expect(td(name, { url: 'https://example.com/a/b?q=1' })).toMatchObject({
                emoji: '📄',
                verb: 'fetch',
                detail: 'example.com',
            });
        });
    }

    for (const name of ['TodoWrite', 'todo_list']) {
        test(`${name}: task count`, () => {
            expect(td(name, { todos: '5 items' })).toMatchObject({ emoji: '📋', verb: 'plan', detail: '5 tasks' });
            expect(td(name, { todos: '5 items' }, undefined, 'ru').detail).toBe('5 задач');
        });
    }

    for (const [name, key] of [['Agent', 'description'], ['Task', 'description'], ['delegate_task', 'goal']]) {
        test(`${name}: ${key}`, () => {
            expect(td(name, { [key]: 'fix tests' })).toMatchObject({ emoji: '🔀', verb: 'delegate', detail: 'fix tests' });
        });
    }

    for (const name of ['Skill', 'skill_view']) {
        test(`${name}: name`, () => {
            expect(td(name, { name: 'pdf' })).toMatchObject({ emoji: '📚', verb: 'skill', detail: 'pdf' });
        });
    }

    test('MCP tool: server as verb, tool + main arg', () => {
        expect(td('mcp__cm__list_sessions', { query: 'x' })).toMatchObject({
            emoji: '🧩',
            verb: 'cm',
            detail: 'list_sessions x',
        });
        expect(td('mcp__cm__list_sessions').detail).toBe('list_sessions');
    });

    test('unknown tool: ⚡, name, first of query/text/command/path/name/prompt', () => {
        expect(td('Frobnicate', { path: '/x', text: 'hello' })).toMatchObject({
            emoji: '⚡',
            verb: 'Frobnicate',
            detail: 'hello',
        });
        expect(td('Frobnicate', undefined, 'raw input').detail).toBe('raw input');
    });

    test('falls back to tool_input without tool_args', () => {
        expect(td('Read', undefined, '/a/b/main.go').detail).toBe('main.go');
        expect(td('Bash', undefined, 'go build ./...').detail).toBe('go build ./...');
    });
});

test.describe('summarizeShellCommand', () => {
    test('drops cd/export/source/true, redirects and tail stages', () => {
        expect(summarizeShellCommand('cd x && npm test | tail -5')).toBe('npm test');
        expect(summarizeShellCommand('export A=1; source env.sh; go vet ./... 2>&1 | head -20')).toBe('go vet ./...');
        expect(summarizeShellCommand('make build > /dev/null')).toBe('make build');
    });

    test('several remaining commands: first + N', () => {
        expect(summarizeShellCommand('go build && go vet && go test')).toBe('go build + 2');
    });

    test('only setup segments: falls back to the raw command', () => {
        expect(summarizeShellCommand('cd x')).toBe('cd x');
    });
});

test.describe('shortenPath', () => {
    test('keeps short paths', () => {
        expect(shortenPath('src/a.go')).toBe('src/a.go');
    });
    test('truncates from the start on a separator', () => {
        const p = '/home/user/projects/claude-manager/internal/session/parser_long_name.go';
        const s = shortenPath(p);
        expect(s.length).toBeLessThanOrEqual(60);
        expect(s.startsWith('…/')).toBe(true);
        expect(s.endsWith('internal/session/parser_long_name.go')).toBe(true);
    });
});

test.describe('group of Hermes read_file calls', () => {
    test('is a read-only group with file names', () => {
        let seq = 0;
        const call = (path: string): LogEntry => ({
            time: new Date(2026, 0, 1, 0, 0, ++seq).toISOString(),
            level: 'tool',
            source: 'hermes',
            message: '',
            tool_name: 'read_file',
            tool_input: path,
            tool_args: { path },
            seq,
        });
        const blocks = groupEntries([call('/x/a.go'), call('/x/b.go')]);
        expect(blocks).toHaveLength(1);
        expect(blocks[0].summary?.readOnly).toBe(true);
        expect(blocks[0].summary?.callLabels).toEqual(['a.go', 'b.go']);
        expect(blocks[0].summary?.firstEmoji).toBe('📖');
    });
});
