/**
 * Unit spec for log entry styling (src/lib/formatters.ts).
 *
 * The load-bearing invariant is legibility on *both* themes: the palette was
 * originally picked against the dark background, and a bare 300/400 tint or a
 * status-* token on the light theme's white panel is barely readable. Every
 * colour must therefore come as a light base class plus a `dark:` variant.
 */

import { test, expect } from '@playwright/test';
import { logEntryColor, logEntryIcon, type LogEntryLike } from '../src/lib/formatters';

// Tints that only work on a dark background.
const DARK_ONLY = /(?:-(?:200|300|400)\b|status-)/;
// Classes that follow the theme CSS vars, so they need no variant.
const THEME_TOKENS = /^text-text(?:-muted|-dim)?$/;

const ENTRIES: LogEntryLike[] = [
    { level: 'error' },
    { level: 'result' },
    { level: 'system' },
    { level: 'thinking' },
    { level: 'cost' },
    { level: 'user' },
    { level: 'tool_result' },
    { level: 'text' },
    { level: '' },
    { level: 'something-new' },
    { level: 'tool', tool_name: 'Read' },
    { level: 'tool', tool_name: 'Bash' },
    { level: 'tool', tool_name: 'Edit' },
    { level: 'tool', tool_name: 'Task' },
    { level: 'tool', tool_name: 'UnknownTool' },
];

test.describe('logEntryColor', () => {
    for (const e of ENTRIES) {
        const label = e.tool_name ? `${e.level}/${e.tool_name}` : e.level || '(unset)';
        test(`${label}: no dark-only tint without a light counterpart`, () => {
            const classes = logEntryColor(e).split(/\s+/).filter(Boolean);
            const base = classes.filter((c) => !c.startsWith('dark:'));
            // At least one colour class that works on white.
            const baseColors = base.filter((c) => c.startsWith('text-'));
            expect(baseColors.length).toBeGreaterThan(0);
            for (const c of baseColors) {
                if (THEME_TOKENS.test(c)) continue;
                expect(c, `${c} is a dark-theme tint used as the light-theme base`).not.toMatch(
                    DARK_ONLY,
                );
            }
        });
    }

    test('keeps the dark-theme look as a variant', () => {
        expect(logEntryColor({ level: 'user' })).toBe('text-amber-700 dark:text-amber-300');
        expect(logEntryColor({ level: 'error' })).toBe('text-red-600 dark:text-status-error');
        expect(logEntryColor({ level: 'tool', tool_name: 'Bash' })).toBe(
            'text-green-700 dark:text-status-working',
        );
    });

    test('theme-token levels are left alone', () => {
        expect(logEntryColor({ level: 'result' })).toBe('text-text-muted');
        expect(logEntryColor({ level: 'thinking' })).toBe('text-text-dim italic');
    });

    test('unknown level falls back to muted text', () => {
        expect(logEntryColor({ level: 'something-new' })).toBe('text-text-muted');
        expect(logEntryColor({})).toBe('text-text-muted');
    });
});

test.describe('logEntryIcon', () => {
    test('one icon per level', () => {
        expect(logEntryIcon({ level: 'error' })).toBe('✖');
        expect(logEntryIcon({ level: 'result' })).toBe('✓');
        expect(logEntryIcon({ level: 'user' })).toBe('›');
        expect(logEntryIcon({ level: 'tool', tool_name: 'Bash' })).toBe('$');
        expect(logEntryIcon({ level: 'tool', tool_name: 'Read' })).toBe('🔍');
        expect(logEntryIcon({ level: 'tool', tool_name: 'Whatever' })).toBe('🔧');
        expect(logEntryIcon({})).toBe('💬');
    });
});
