/**
 * LogStream markdown rendering.
 *
 * Drives the real DOM: selects a session, pushes one log entry whose message
 * is markdown through the same `session:log` event the backend emits, and
 * checks that the "Markdown" checkbox above the log switches between the
 * formatted body and the raw text — and that a markdown message comes up
 * expanded rather than collapsed to its first line.
 *
 * The entry is injected via the bridge's __dispatchWailsEvent instead of a
 * fakeclaude scenario: what is under test is purely the renderer's DOM, and a
 * scenario would tie it to a session lifecycle it does not depend on.
 */

import { test, expect } from './fixtures';
import type { Page } from '@playwright/test';

const toggleSel = 'label:has-text("Markdown") input[type="checkbox"]';

const SESSION_ID = 'test/S1';

const MARKDOWN = [
    '## Report',
    '',
    'Some **bold** prose with `code` and a [link](https://example.com).',
    '',
    '- first item',
    '- second item',
    '',
    '| Step | Result |',
    '|---|---|',
    '| type | ok |',
    '',
    '```go',
    'if a < b {}',
    '```',
].join('\n');

async function pushLog(page: Page, message: string, level = 'text') {
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
            },
        },
    );
}

async function openSession(page: Page) {
    await page.goto('/');
    await selectSession(page);
}

// Click the name label rather than the row: the row's centre is the model
// <select>, and clicking that would open the dropdown instead of selecting.
async function selectSession(page: Page) {
    const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
    await expect(row).toBeVisible({ timeout: 5_000 });
    await row.getByText('S1', { exact: true }).click();
    await expect(page.locator(toggleSel)).toBeVisible({
        timeout: 5_000,
    });
}

test.describe('LogStream markdown', () => {
    // SessionView (and with it LogStream) only renders for a session the
    // backend knows about — a configured-but-never-started session is just a
    // sidebar placeholder. Start it once; the happy-path scenario finishes on
    // its own and the session stays listed as idle.
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

    test('renders markdown formatted and expanded, raw when unchecked', async ({ page }) => {
        await openSession(page);

        const toggle = page.locator(toggleSel);
        await expect(toggle).toBeVisible({ timeout: 5_000 });
        await expect(toggle).toBeChecked(); // formatted is the default

        await pushLog(page, MARKDOWN);

        const body = page.locator('.md-body').first();
        await expect(body).toBeVisible({ timeout: 5_000 });

        // Every block type made it through, and the entry is expanded (a
        // collapsed entry would show only its first line, "## Report").
        await expect(body.locator('h2')).toHaveText('Report');
        await expect(body.locator('strong')).toHaveText('bold');
        await expect(body.locator('code').first()).toHaveText('code');
        await expect(body.locator('li')).toHaveCount(2);
        await expect(body.locator('table td').first()).toHaveText('type');
        await expect(body.locator('pre code')).toContainText('if a < b {}');
        await expect(body.locator('a')).toHaveAttribute('href', 'https://example.com');

        // Raw mode: no rendered body, the markup shows verbatim.
        await toggle.uncheck();
        await expect(page.locator('.md-body')).toHaveCount(0);
        await expect(page.locator('text=**bold**').first()).toBeVisible();

        // And back.
        await toggle.check();
        await expect(page.locator('.md-body').first()).toBeVisible();
    });

    test('plain messages are unaffected by the toggle', async ({ page }) => {
        await openSession(page);
        await pushLog(page, 'Working on the task...');

        await expect(page.locator('text=Working on the task...').first()).toBeVisible({
            timeout: 5_000,
        });
        await expect(page.locator('.md-body')).toHaveCount(0);
    });

    test('tool output is never reinterpreted as markdown', async ({ page }) => {
        await openSession(page);
        // A shell transcript whose comment lines look like ATX headings.
        await pushLog(page, '# build the thing\n$ go build ./...', 'tool_result');

        await expect(page.locator('text=# build the thing').first()).toBeVisible({
            timeout: 5_000,
        });
        await expect(page.locator('.md-body')).toHaveCount(0);
    });

    test('the choice survives a reload', async ({ page }) => {
        await openSession(page);

        const toggle = page.locator(toggleSel);
        await expect(toggle).toBeVisible({ timeout: 5_000 });
        await toggle.uncheck();

        await page.reload();
        await selectSession(page);

        await expect(
            page.locator(toggleSel),
        ).not.toBeChecked();
    });
});
