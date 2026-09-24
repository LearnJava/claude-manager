/**
 * Feed layout — thinking/prose blocks (VIEW-TASKS.md UI-03).
 *
 * Pushes a thinking + tool-call + prose sequence through the same
 * session:log bridge event log-markdown.spec.ts uses (session logs are only
 * ever built from live events in the frontend store, so a page reload after
 * the real fakeclaude run has already finished leaves nothing to look at —
 * injecting directly is what actually exercises the renderer). Runs once per
 * theme so a dark-only tint slipping into the new blocks would be caught the
 * same way formatters.spec.ts catches it for the classic view.
 */

import { test, expect } from './fixtures';
import type { Page } from '@playwright/test';

const SESSION_ID = 'test/S1';
const feedToggleSel = 'label:has-text("Feed") input[type="checkbox"]';

async function pushLog(page: Page, level: string, message: string, extra: Record<string, unknown> = {}) {
    await page.evaluate(
        ({ id, entry }) => {
            const w = window as typeof window & {
                __dispatchWailsEvent: (n: string, d: unknown) => void;
            };
            w.__dispatchWailsEvent('session:log', { id, entry });
        },
        {
            id: SESSION_ID,
            entry: {
                time: new Date().toISOString(),
                level,
                source: 'claude',
                message,
                ...extra,
            },
        },
    );
}

async function openFeed(page: Page, theme: 'dark' | 'light') {
    await page.addInitScript((t) => {
        try {
            window.localStorage.setItem('cm.theme', t);
        } catch (_) {
            /* ignore */
        }
    }, theme);
    await page.goto('/');
    const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
    await expect(row).toBeVisible({ timeout: 5_000 });
    await row.getByText('S1', { exact: true }).click();

    const toggle = page.locator(feedToggleSel);
    await expect(toggle).toBeVisible({ timeout: 5_000 });
    await toggle.check();
}

for (const theme of ['dark', 'light'] as const) {
    test.describe(`Feed layout (${theme})`, () => {
        test('thinking collapses to a muted "Thought for Ns" line', async ({ page }) => {
            await openFeed(page, theme);

            const now = Date.now();
            await pushLog(page, 'thinking', 'Let me look at the failing test first.');
            // A second entry closes the thinking block and gives it a duration.
            await page.waitForTimeout(50);
            await pushLog(page, 'tool', 'Read: parser.go', { tool_name: 'Read', tool_input: 'parser.go' });

            const thinkingRow = page.locator('button', { hasText: /Thought for|Thinking/ }).first();
            await expect(thinkingRow).toBeVisible({ timeout: 5_000 });
            // Collapsed by default — the raw thinking text is not in the DOM yet.
            await expect(page.locator('text=Let me look at the failing test first.')).toHaveCount(0);

            await thinkingRow.click();
            await expect(page.locator('text=Let me look at the failing test first.')).toBeVisible();
            void now;
        });

        test('prose result renders proportional font with markdown', async ({ page }) => {
            await openFeed(page, theme);

            await pushLog(page, 'text', '## Summary\n\nThe parser looks correct. **No changes needed.**');

            const body = page.locator('.md-body').first();
            await expect(body).toBeVisible({ timeout: 5_000 });
            await expect(body.locator('h2')).toHaveText('Summary');
            await expect(body.locator('strong')).toHaveText('No changes needed.');
            // Proportional, not font-mono — the classic view's monospace class
            // must not leak into a prose block.
            const proseContainer = page.locator('.font-sans.md-body, .font-sans:has(.md-body)').first();
            await expect(proseContainer).toBeVisible();
        });

        test('a file edit renders as its own card with +N/-M and an expandable diff', async ({ page }) => {
            await openFeed(page, theme);

            await pushLog(page, 'tool', 'Edit: /src/store_test.go', {
                tool_name: 'Edit',
                tool_input: '/src/store_test.go',
                diff: {
                    added: 3,
                    removed: 1,
                    lines: [
                        { type: 'del', text: 'old line' },
                        { type: 'add', text: 'new line 1' },
                        { type: 'add', text: 'new line 2' },
                        { type: 'add', text: 'new line 3' },
                    ],
                },
            });

            const card = page.locator('button', { hasText: 'store_test.go' }).first();
            await expect(card).toBeVisible({ timeout: 5_000 });
            await expect(card).toContainText('+3');
            await expect(card).toContainText('−1');
            await expect(page.locator('text=new line 1')).toHaveCount(0);

            await card.click();
            await expect(page.locator('text=new line 1')).toBeVisible();
            await expect(page.locator('text=old line')).toBeVisible();
        });
    });
}
