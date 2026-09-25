/**
 * Unit spec for turnSummaryData + the turn summary line (VIEW-TASKS.md UI-13).
 */

import { test, expect } from './fixtures';
import { test as unit, expect as uexpect } from '@playwright/test';
import type { Page } from '@playwright/test';
import { turnSummaryData } from '../src/lib/logGroups';

unit.describe('turnSummaryData', () => {
    unit('no turn -> null', () => {
        uexpect(turnSummaryData(undefined)).toBeNull();
    });

    unit('claude success: duration, turns, tokens, cost', () => {
        const d = turnSummaryData({
            duration_ms: 42_000, num_turns: 12, total_cost_usd: 0.18, subtype: 'success',
            usage: { input_tokens: 1000, output_tokens: 500, cache_read_input_tokens: 30000, cache_creation_input_tokens: 3500 },
        });
        uexpect(d).toEqual({ ok: true, durationMs: 42_000, numTurns: 12, tokens: 35_000, costUsd: 0.18, error: undefined });
    });

    unit('hermes: only duration and tokens', () => {
        const d = turnSummaryData({ duration_ms: 7173, usage: { input_tokens: 8, output_tokens: 63 } });
        uexpect(d?.ok).toBe(true);
        uexpect(d?.tokens).toBe(71);
        uexpect(d?.numTurns).toBeUndefined();
        uexpect(d?.costUsd).toBeUndefined();
    });

    unit('failure: subtype, else result text', () => {
        uexpect(turnSummaryData({ subtype: 'error_max_turns' })).toMatchObject({ ok: false, error: 'error_max_turns' });
        uexpect(turnSummaryData({ stop_reason: 'error', result: 'boom' })).toMatchObject({ ok: false, error: 'boom' });
    });
});

const SESSION_ID = 'test/S1';

async function dispatch(page: Page, name: string, data: unknown) {
    await page.evaluate(
        ({ name, data }) => {
            const w = window as typeof window & { __dispatchWailsEvent: (n: string, d: unknown) => void };
            w.__dispatchWailsEvent(name, data);
        },
        { name, data },
    );
}

test.describe('Turn summary line', () => {
    test('shown after result in either event order; failure is red', async ({ page }) => {
        await page.goto('/');
        const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
        await expect(row).toBeVisible({ timeout: 5_000 });
        await row.getByText('S1', { exact: true }).click();
        const toggle = page.locator('label:has-text("Feed") input[type="checkbox"]');
        await expect(toggle).toBeVisible({ timeout: 5_000 });
        await toggle.check();

        const resultEntry = (msg: string) => ({
            id: SESSION_ID,
            entry: { time: new Date().toISOString(), level: 'result', source: 'claude', message: msg },
        });

        // log entry first, session:result second
        await dispatch(page, 'session:log', resultEntry('turn one done'));
        await dispatch(page, 'session:result', {
            id: SESSION_ID,
            result: { duration_ms: 42_000, num_turns: 12, total_cost_usd: 0.18, subtype: 'success',
                usage: { input_tokens: 1000, output_tokens: 34_000 } },
        });
        const lines = page.getByTestId('turn-summary');
        await expect(lines).toHaveCount(1);
        await expect(lines.first()).toContainText('42s');
        await expect(lines.first()).toContainText('12');
        await expect(lines.first()).toContainText('35.0k');

        // session:result first (Hermes: no turns/cost), log entry second
        await dispatch(page, 'session:result', {
            id: SESSION_ID,
            result: { duration_ms: 7000, usage: { input_tokens: 8, output_tokens: 63 } },
        });
        await dispatch(page, 'session:log', resultEntry('turn two done'));
        await expect(lines).toHaveCount(2);
        await expect(lines.nth(1)).toContainText('7.0s');
        await expect(lines.nth(1)).not.toContainText('$');

        await dispatch(page, 'session:log', resultEntry('turn three'));
        await dispatch(page, 'session:result', { id: SESSION_ID, result: { subtype: 'error_max_turns' } });
        await expect(lines).toHaveCount(3);
        await expect(lines.nth(2)).toContainText('error_max_turns');
        await expect(lines.nth(2)).toHaveClass(/red/);
    });
});
