/**
 * Feed layout — one row per tool call in an expanded group (VIEW-TASKS.md UI-11).
 * Entries are injected through the session:log bridge, as in log-feed.spec.ts.
 */

import { test, expect } from './fixtures';
import type { Page } from '@playwright/test';

const SESSION_ID = 'test/S1';

async function pushLog(page: Page, level: string, message: string, extra: Record<string, unknown> = {}) {
    await page.evaluate(
        ({ id, entry }) => {
            const w = window as typeof window & { __dispatchWailsEvent: (n: string, d: unknown) => void };
            w.__dispatchWailsEvent('session:log', { id, entry });
        },
        { id: SESSION_ID, entry: { time: new Date().toISOString(), level, source: 'claude', message, ...extra } },
    );
}

async function openFeed(page: Page, theme: 'dark' | 'light') {
    await page.addInitScript((t) => window.localStorage.setItem('cm.theme', t), theme);
    await page.goto('/');
    const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
    await expect(row).toBeVisible({ timeout: 5_000 });
    await row.getByText('S1', { exact: true }).click();
    const toggle = page.locator('label:has-text("Feed") input[type="checkbox"]');
    await expect(toggle).toBeVisible({ timeout: 5_000 });
    await toggle.check();
}

async function pushCalls(page: Page) {
    await pushLog(page, 'tool', 'Read: main.go', {
        tool_name: 'Read', tool_use_id: 'c1', tool_args: { file_path: '/a/main.go', offset: '10', limit: '40' },
    });
    await pushLog(page, 'tool_result', 'package main SECRETOUTPUT', { tool_use_id: 'c1', duration_ms: 300 });
    await pushLog(page, 'tool', 'Bash: go test', { tool_name: 'Bash', tool_use_id: 'c2', tool_args: { command: 'go test ./...' } });
    await pushLog(page, 'error', 'FAIL: boom in parser', { tool_use_id: 'c2', duration_ms: 65_000 });
    await pushLog(page, 'tool', 'Grep: foo', { tool_name: 'Grep', tool_use_id: 'c3', tool_args: { pattern: 'foo' } });
}

for (const theme of ['dark', 'light'] as const) {
    test.describe(`ToolCallRow (${theme})`, () => {
        test('rows show emoji, duration and status; click reveals output', async ({ page }) => {
            await openFeed(page, theme);
            await pushCalls(page);
            await expect(page.getByTestId('group-spinner')).toBeVisible();
            await page.locator('button', { hasText: 'main.go' }).first().click();

            const rows = page.getByTestId('tool-call-row');
            await expect(rows).toHaveCount(3);
            await expect(rows.nth(0)).toContainText('📖');
            await expect(rows.nth(0)).toContainText('main.go L10-49');
            await expect(rows.nth(0)).toContainText('0.3s');
            await expect(rows.nth(0)).toContainText('✓');
            await expect(rows.nth(1)).toContainText('1m05s');
            await expect(rows.nth(1)).toContainText('✖');
            await expect(rows.nth(1)).toContainText('FAIL: boom in parser');
            await expect(rows.nth(2).getByTestId('tool-spinner')).toBeVisible();

            await expect(page.locator('text=SECRETOUTPUT')).toHaveCount(0);
            await rows.nth(0).locator('button').first().click();
            await expect(page.locator('text=SECRETOUTPUT').first()).toBeVisible();
        });

        test('search on output expands group and call row', async ({ page }) => {
            await openFeed(page, theme);
            await pushCalls(page);
            await page.locator('input[type="search"]').fill('SECRETOUTPUT');
            await expect(page.locator('text=SECRETOUTPUT').first()).toBeVisible();
        });
    });
}
