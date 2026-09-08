/**
 * Unit spec for the token helpers (src/lib/formatters.ts).
 *
 * Two shapes of the same numbers reach the UI: the snake_case one from
 * SessionState / SessionMetrics / DailyTokens, and the Go-exported PascalCase
 * one from store.SessionRun rows (GetHistory). The dashboard sums run rows
 * while the sidebar sums live session state, so a helper that understands only
 * one of them silently reports zero on half the screens — which is exactly the
 * failure the shared helper exists to prevent.
 */

import { test, expect } from '@playwright/test';
import { tokenSplit, totalTokens, tokenSplitLabel } from '../src/lib/formatters';

const SNAKE = {
    input_tokens: 1000,
    output_tokens: 200,
    cache_read: 50_000,
    cache_creation: 3000,
};

const PASCAL = {
    InputTokens: 1000,
    OutputTokens: 200,
    CacheReadTokens: 50_000,
    CacheCreationTokens: 3000,
};

test.describe('tokenSplit', () => {
    test('reads the snake_case shape', () => {
        expect(tokenSplit(SNAKE)).toEqual({
            input: 1000,
            output: 200,
            cacheRead: 50_000,
            cacheCreation: 3000,
            total: 54_200,
        });
    });

    test('reads the PascalCase run-row shape identically', () => {
        expect(tokenSplit(PASCAL)).toEqual(tokenSplit(SNAKE));
    });

    test('total counts cache traffic, not just input and output', () => {
        // The whole point of leading with tokens: cache reads usually dominate
        // the volume, and a total that ignored them would understate a run by
        // an order of magnitude.
        expect(totalTokens(SNAKE)).toBe(54_200);
        expect(totalTokens(SNAKE)).toBeGreaterThan(SNAKE.input_tokens + SNAKE.output_tokens);
    });

    test('missing, null and NaN fields count as zero', () => {
        expect(tokenSplit(undefined).total).toBe(0);
        expect(tokenSplit(null).total).toBe(0);
        expect(tokenSplit({}).total).toBe(0);
        expect(tokenSplit({ input_tokens: NaN, output_tokens: 5 }).total).toBe(5);
        expect(tokenSplit({ input_tokens: '12' as any }).total).toBe(0);
    });

    test('a partially-filled row still sums the fields it has', () => {
        expect(tokenSplit({ InputTokens: 7, cache_read: 3 }).total).toBe(10);
    });
});

test.describe('tokenSplitLabel', () => {
    test('names all four kinds so the total is never read alone', () => {
        const label = tokenSplitLabel(SNAKE);
        expect(label).toContain('in');
        expect(label).toContain('out');
        expect(label).toContain('cache read');
        expect(label).toContain('cache write');
        expect(label).toContain('50.0k');
    });

    test('is safe on an empty source', () => {
        expect(tokenSplitLabel(undefined)).toContain('0 in');
    });
});
