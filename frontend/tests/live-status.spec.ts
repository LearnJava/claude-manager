/**
 * Unit spec for src/lib/liveStatus.ts + the live status line (VIEW-TASKS.md UI-12).
 */

import { test, expect } from './fixtures';
import { test as unit, expect as uexpect } from '@playwright/test';
import type { Page } from '@playwright/test';
import { liveStatusLine, thinkingVerb, spinnerFrame, lastOpenCall, SPINNER_FRAMES } from '../src/lib/liveStatus';

const T0 = Date.parse('2026-01-01T00:00:00Z');
const since = new Date(T0).toISOString();

unit.describe('liveStatus', () => {
    unit('idle / missing activity -> nothing', () => {
        uexpect(liveStatusLine(undefined, T0, 'en')).toBeNull();
        uexpect(liveStatusLine({ kind: 'idle', since }, T0, 'en')).toBeNull();
    });

    unit('thinking: verb rotates, time formatted', () => {
        const a = { kind: 'thinking' as const, since };
        uexpect(liveStatusLine(a, T0 + 4000, 'ru')).toEqual({ text: 'Анализирую…', elapsed: '4s' });
        uexpect(thinkingVerb(0, 'en')).toBe('Pondering');
        uexpect(thinkingVerb(4000, 'en')).toBe('Reasoning');
        uexpect(thinkingVerb(4000 * 5, 'en')).toBe('Pondering');
        uexpect(liveStatusLine(a, T0 + 65_000, 'en')?.elapsed).toBe('1m05s');
    });

    unit('writing', () => {
        uexpect(liveStatusLine({ kind: 'writing', since }, T0 + 2000, 'ru')).toEqual({ text: '✍ Пишет ответ…', elapsed: '2s' });
    });

    unit('tool: running phrase of the open call, fallback by name', () => {
        const a = { kind: 'tool' as const, tool: 'Bash', since };
        const l = liveStatusLine(a, T0 + 12_000, 'en', { tool_name: 'Bash', tool_args: { command: 'npm test' } });
        uexpect(l?.text).toContain('npm test');
        uexpect(l?.elapsed).toBe('12s');
        uexpect(liveStatusLine(a, T0, 'ru')?.text).toBe('💻 Готовлю Bash…');
    });

    unit('spinner frames cycle', () => {
        uexpect(spinnerFrame(0)).toBe(SPINNER_FRAMES[0]);
        uexpect(spinnerFrame(100)).toBe(SPINNER_FRAMES[1]);
        uexpect(spinnerFrame(1000)).toBe(SPINNER_FRAMES[0]);
    });

    unit('lastOpenCall', () => {
        const c1 = { level: 'tool', tool_use_id: 'a', tool_name: 'Read' };
        const c2 = { level: 'tool', tool_use_id: 'b', tool_name: 'Bash' };
        uexpect(lastOpenCall([c1, c2])).toBe(c2);
        const rb = { level: 'tool_result', tool_use_id: 'b' };
        uexpect(lastOpenCall([c1, c2, rb])).toBe(c1);
        uexpect(lastOpenCall([c1, c2, rb, { level: 'error', tool_use_id: 'a' }])).toBeNull();
        uexpect(lastOpenCall([c1, c2, { level: 'result' }])).toBeNull();
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

test.describe('LiveStatus line', () => {
    test('thinking -> tool -> gone after idle', async ({ page }) => {
        await page.goto('/');
        const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
        await expect(row).toBeVisible({ timeout: 5_000 });
        await row.getByText('S1', { exact: true }).click();
        const toggle = page.locator('label:has-text("Feed") input[type="checkbox"]');
        await expect(toggle).toBeVisible({ timeout: 5_000 });
        await toggle.check();

        await dispatch(page, 'session:status', { id: SESSION_ID, status: 'working' });
        await dispatch(page, 'session:activity', { id: SESSION_ID, kind: 'thinking', since: new Date().toISOString() });
        const line = page.getByTestId('live-status');
        await expect(line).toBeVisible();
        await expect(line).toHaveAttribute('data-kind', 'thinking');

        await dispatch(page, 'session:log', {
            id: SESSION_ID,
            entry: { time: new Date().toISOString(), level: 'tool', source: 'claude', message: 'Bash: npm test',
                tool_name: 'Bash', tool_use_id: 'x1', tool_args: { command: 'npm test' } },
        });
        await dispatch(page, 'session:activity', { id: SESSION_ID, kind: 'tool', tool: 'Bash', since: new Date().toISOString() });
        await expect(line).toHaveAttribute('data-kind', 'tool');
        await expect(line).toContainText('npm test');

        await dispatch(page, 'session:activity', { id: SESSION_ID, kind: 'idle', since: new Date().toISOString() });
        await expect(line).toHaveCount(0);
    });
});
