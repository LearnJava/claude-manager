/**
 * Unit spec for the message box's "/" autocomplete helpers
 * (src/lib/slashCommands.ts).
 */

import { test, expect } from '@playwright/test';
import {
    applySlashCommand,
    filterSlashCommands,
    slashQuery,
    type SlashCommand,
} from '../src/lib/slashCommands';

test.describe('slashQuery', () => {
    test('open while the caret is inside the leading /token', () => {
        expect(slashQuery('/', 1)).toBe('');
        expect(slashQuery('/com', 4)).toBe('com');
        expect(slashQuery('/com', 2)).toBe('com');
        expect(slashQuery('/compact rest', 3)).toBe('compact');
    });

    test('closed otherwise', () => {
        expect(slashQuery('', 0)).toBeNull();
        expect(slashQuery('hello /x', 8)).toBeNull();
        expect(slashQuery('/compact ', 9)).toBeNull();
        expect(slashQuery('/compact rest', 12)).toBeNull();
    });
});

test.describe('filterSlashCommands', () => {
    const list: SlashCommand[] = [
        { name: 'code-review', kind: 'skill' },
        { name: 'compact', kind: 'command' },
        { name: 'plugin:review-pr', kind: 'skill' },
        { name: 'security-review', kind: 'command' },
    ];

    test('empty query keeps the whole catalog', () => {
        expect(filterSlashCommands(list, '')).toEqual(list);
    });

    test('prefix hits (incl. after a namespace) come before substring hits', () => {
        expect(filterSlashCommands(list, 'REV').map((c) => c.name)).toEqual([
            'plugin:review-pr',
            'code-review',
            'security-review',
        ]);
        expect(filterSlashCommands(list, 'co').map((c) => c.name)).toEqual(['code-review', 'compact']);
        expect(filterSlashCommands(list, 'zzz')).toEqual([]);
    });
});

test.describe('applySlashCommand', () => {
    test('replaces the token and keeps the tail', () => {
        expect(applySlashCommand('/', 'compact')).toBe('/compact ');
        expect(applySlashCommand('/co', 'compact')).toBe('/compact ');
        expect(applySlashCommand('/co   keep this', 'compact')).toBe('/compact keep this');
        expect(applySlashCommand('/co\nnext line', 'compact')).toBe('/compact \nnext line');
    });
});
