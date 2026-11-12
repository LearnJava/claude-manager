/**
 * LogStream compact tool-call lines (the "Tools" checkbox).
 *
 * Drives the real DOM like log-markdown.spec.ts: pushes one level='tool'
 * entry through session:log and checks that the checkbox swaps the raw input
 * dump for a one-line toolDisplay summary, that the row's + toggle still
 * reaches the full raw entry, and that the choice persists across a reload.
 *
 * Classic layout on purpose: an orphan 'tool' entry in the feed starts a
 * collapsed tools group (logGroups adjacency), which would test grouping,
 * not the per-row rendering this file is about.
 */

import { test, expect } from './fixtures';
import type { Page } from '@playwright/test';

const toggleSel = 'label:has-text("Tools") input[type="checkbox"]';
const feedToggleSel = 'label:has-text("Feed") input[type="checkbox"]';

const SESSION_ID = 'test/S1';

const RAW_DUMP =
    'write: {"path":"tmp-probe-dialog.html","content":"<!DOCTYPE html>\\n<html>\\n<head><meta charset=utf-8></head>\\n<body><dialog>probe</dialog></body>\\n</html>"}';

const TOOL_ENTRY = {
    time: new Date().toISOString(),
    level: 'tool',
    source: 'claude',
    message: RAW_DUMP,
    tool_name: 'Write',
    tool_args: { file_path: 'tmp-probe-dialog.html' },
};

async function pushEntry(page: Page, entry: Record<string, unknown>) {
    await page.evaluate(
        ({ id, entry }) => {
            const w = window as typeof window & {
                __dispatchWailsEvent: (n: string, d: unknown) => void;
            };
            w.__dispatchWailsEvent('session:log', { id, entry });
        },
        { id: SESSION_ID, entry },
    );
}

async function selectSession(page: Page) {
    const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
    await expect(row).toBeVisible({ timeout: 5_000 });
    await row.getByText('S1', { exact: true }).click();
    await expect(page.locator(toggleSel)).toBeVisible({ timeout: 5_000 });
    const feedToggle = page.locator(feedToggleSel);
    if (await feedToggle.isChecked()) {
        await feedToggle.uncheck();
    }
}

async function openSession(page: Page) {
    await page.goto('/');
    await selectSession(page);
}

test.describe('LogStream compact tools', () => {
    test.beforeEach(async ({ ctrl }) => {
        await ctrl.rpc('StartSession', { project: 'test', session: 'S1' }).catch(() => {
            /* already running */
        });
        await ctrl
            .wait('session:status', { id: SESSION_ID, status: 'working' }, 12_000)
            .catch(() => {
                /* already past working */
            });
    });

    test('tool call shows a one-line summary by default, raw when unchecked', async ({ page }) => {
        await openSession(page);

        const toggle = page.locator(toggleSel);
        await expect(toggle).toBeChecked(); // pretty is the default

        await pushEntry(page, TOOL_ENTRY);

        await expect(page.getByText('write tmp-probe-dialog.html')).toBeVisible();
        await expect(page.getByText('write: {"path"')).toHaveCount(0);

        await toggle.uncheck();
        await expect(page.getByText('write: {"path"').first()).toBeVisible();
        await expect(page.getByText('write tmp-probe-dialog.html')).toHaveCount(0);

        await toggle.check();
        await expect(page.getByText('write tmp-probe-dialog.html').first()).toBeVisible();
    });

    test('the + toggle still reaches the full raw entry', async ({ page }) => {
        await openSession(page);
        await pushEntry(page, TOOL_ENTRY);

        await expect(page.getByText('write tmp-probe-dialog.html').first()).toBeVisible();
        await page.locator('button[title="Expand"]').first().click();
        await expect(page.getByText('write: {"path"').first()).toBeVisible();
    });

    test('plain entries are unaffected by the toggle', async ({ page }) => {
        await openSession(page);
        await pushEntry(page, {
            time: new Date().toISOString(),
            level: 'text',
            source: 'claude',
            message: 'Thinking about the fix.',
        });

        await expect(page.getByText('Thinking about the fix.')).toBeVisible();
    });

    test('the choice survives a reload', async ({ page }) => {
        await openSession(page);

        const toggle = page.locator(toggleSel);
        await toggle.uncheck();
        await page.reload();
        await selectSession(page);

        await expect(page.locator(toggleSel)).not.toBeChecked();
    });
});
