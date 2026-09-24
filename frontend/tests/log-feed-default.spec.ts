/**
 * Feed layout, round out (VIEW-TASKS.md UI-05):
 *  - the feed is the default LogStream layout for a session with no stored
 *    preference, an existing localStorage choice still wins;
 *  - Ctrl+F search finds and auto-expands a hit inside a collapsed group;
 *  - an errored tool call surfaces its ✖ on the collapsed group header;
 *  - autoscroll (pendingStick/prevLastSeq) keeps pinning to the bottom in the
 *    feed the same way it does in the classic view, including across the
 *    ring-buffer eviction boundary.
 *
 * Entries are injected via the same __dispatchWailsEvent bridge log-feed.spec
 * and log-markdown.spec.ts use — session logs only ever come from live
 * events, so this is what actually exercises LogStream's feed rendering.
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

async function selectSession(page: Page) {
    await page.goto('/');
    const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
    await expect(row).toBeVisible({ timeout: 5_000 });
    await row.getByText('S1', { exact: true }).click();
}

test.describe('Feed layout is the default (UI-05)', () => {
    test('a fresh session with no stored preference opens in the feed', async ({ page }) => {
        await selectSession(page);
        const toggle = page.locator(feedToggleSel);
        await expect(toggle).toBeVisible({ timeout: 5_000 });
        await expect(toggle).toBeChecked();
    });

    test('an explicit classic choice in localStorage still wins', async ({ page }) => {
        await page.addInitScript(() => {
            try {
                window.localStorage.setItem('cm.logLayout', 'classic');
            } catch (_) {
                /* ignore */
            }
        });
        await selectSession(page);
        const toggle = page.locator(feedToggleSel);
        await expect(toggle).toBeVisible({ timeout: 5_000 });
        await expect(toggle).not.toBeChecked();
    });

    test('Ctrl+F finds and expands a hit inside a collapsed tools group', async ({ page }) => {
        await selectSession(page);
        await expect(page.locator(feedToggleSel)).toBeChecked();

        await pushLog(page, 'tool', 'Bash: echo one', { tool_name: 'Bash', tool_input: 'echo one' });
        await pushLog(page, 'tool_result', 'one', { tool_use_id: undefined });
        await pushLog(page, 'tool', 'Bash: echo needle-token', {
            tool_name: 'Bash',
            tool_input: 'echo needle-token',
        });
        await pushLog(page, 'tool_result', 'needle-token');

        // The group starts collapsed — the match text is not in the DOM yet.
        await expect(page.locator('text=needle-token')).toHaveCount(0);

        await page.keyboard.press('Control+f');
        const search = page.locator('input[type="search"]');
        await expect(search).toBeFocused();
        await search.fill('needle-token');

        // Matching group auto-expands (VIEW-TASKS.md UI-02 "Группа … раскрывается").
        await expect(page.locator('text=needle-token').first()).toBeVisible({ timeout: 5_000 });
    });

    test('an error inside a tools series marks the collapsed header', async ({ page }) => {
        await selectSession(page);
        await expect(page.locator(feedToggleSel)).toBeChecked();

        await pushLog(page, 'tool', 'Bash: false', { tool_name: 'Bash', tool_input: 'false' });
        await pushLog(page, 'error', 'command failed');

        const errMark = page.locator('button', { hasText: '✖' }).first();
        await expect(errMark).toBeVisible({ timeout: 5_000 });
    });

    test('autoscroll pins to the bottom as new feed blocks arrive', async ({ page }) => {
        await selectSession(page);
        await expect(page.locator(feedToggleSel)).toBeChecked();

        for (let i = 0; i < 30; i++) {
            await pushLog(page, 'text', `Update number ${i}`);
        }

        await expect(page.locator('text=Update number 29').first()).toBeVisible({ timeout: 5_000 });
        // Stuck to bottom: the "jump to latest" button must not be showing.
        await expect(page.locator('button', { hasText: 'Jump to latest' })).toHaveCount(0);
    });
});
