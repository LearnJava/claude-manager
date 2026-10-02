/**
 * Copy buttons in the log: the per-entry hover button (LogEntryRow) and the
 * language/copy header above rendered code blocks (lib/codeCopy.ts).
 *
 * navigator.clipboard is replaced by a recorder in an init script: headless
 * Chromium needs extra permissions for the real one, and what is under test
 * is which text the buttons hand over, not the OS clipboard.
 */

import { test, expect } from './fixtures';
import { test as unit, expect as uexpect } from '@playwright/test';
import type { Page } from '@playwright/test';
import { codeLang } from '../src/lib/codeCopy';

const SESSION_ID = 'test/S1';
const feedToggleSel = 'label:has-text("Feed") input[type="checkbox"]';

const ANSWER = [
    'Found it: `parseLine` drops the buffer tail.',
    '',
    '```go',
    'p.buf = append(p.buf, chunk...)',
    'return p.parse(lines)',
    '```',
    '',
    '- test added',
].join('\n');

unit.describe('codeLang', () => {
    unit('reads the lang- class renderMarkdown sets', () => {
        uexpect(codeLang({ className: 'lang-go' } as Element)).toBe('go');
        uexpect(codeLang({ className: 'x lang-ts' } as Element)).toBe('ts');
    });
    unit('no language -> empty', () => {
        uexpect(codeLang({ className: '' } as Element)).toBe('');
        uexpect(codeLang(null)).toBe('');
    });
});

async function stubClipboard(page: Page, fail = false) {
    await page.addInitScript((fail) => {
        const w = window as typeof window & { __copied: string[] };
        w.__copied = [];
        Object.defineProperty(navigator, 'clipboard', {
            configurable: true,
            value: {
                writeText: async (text: string) => {
                    if (fail) throw new Error('denied');
                    w.__copied.push(text);
                },
            },
        });
    }, fail);
}

async function copied(page: Page): Promise<string[]> {
    return page.evaluate(() => (window as typeof window & { __copied: string[] }).__copied);
}

async function pushLog(page: Page, level: string, message: string) {
    await page.evaluate(
        ({ id, entry }) => {
            const w = window as typeof window & { __dispatchWailsEvent: (n: string, d: unknown) => void };
            w.__dispatchWailsEvent('session:log', { id, entry });
        },
        { id: SESSION_ID, entry: { time: new Date().toISOString(), level, source: 'claude', message } },
    );
}

async function openSession(page: Page, layout: 'feed' | 'classic') {
    await page.goto('/');
    const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
    await expect(row).toBeVisible({ timeout: 5_000 });
    await row.getByText('S1', { exact: true }).click();
    const toggle = page.locator(feedToggleSel);
    await expect(toggle).toBeVisible({ timeout: 5_000 });
    if (layout === 'feed') await toggle.check();
    else await toggle.uncheck();
}

test.describe('Log copy buttons', () => {
    test('entry button appears on hover and copies the markdown source', async ({ page }) => {
        await stubClipboard(page);
        await openSession(page, 'feed');
        await pushLog(page, 'text', ANSWER);

        const body = page.locator('.md-body', { hasText: 'Found it' });
        await expect(body).toBeVisible({ timeout: 5_000 });
        const btn = page.locator('[data-testid="entry-copy"]').last();
        await expect(btn).toBeHidden();

        await body.hover();
        await expect(btn).toBeVisible();
        await btn.click();
        await expect.poll(() => copied(page)).toEqual([ANSWER]);
        await expect(btn).toHaveAttribute('title', /Copied/);
        // Feedback reverts, and the button hides again once the mouse leaves.
        await page.mouse.move(0, 0);
        await expect(btn).toHaveAttribute('title', /Copy this entry/, { timeout: 3_000 });
        await expect(btn).toBeHidden();
    });

    test('collapsed classic row copies the full message, not its summary', async ({ page }) => {
        await stubClipboard(page);
        await openSession(page, 'classic');
        const msg = 'first line of output\nsecond line\nthird line';
        await pushLog(page, 'tool_result', msg);

        const row = page.locator('div.group\\/row', { hasText: 'first line of output' }).last();
        await expect(row).toContainText('…');
        await row.hover();
        await row.locator('[data-testid="entry-copy"]').click();
        await expect.poll(() => copied(page)).toEqual([msg]);
    });

    test('code block gets a language header that copies only the code', async ({ page }) => {
        await stubClipboard(page);
        await openSession(page, 'feed');
        await pushLog(page, 'text', ANSWER);

        const head = page.locator('.md-body .code-head');
        await expect(head).toHaveCount(1, { timeout: 5_000 });
        await expect(head.locator('span')).toHaveText('go');
        await head.locator('.code-copy').click();
        await expect.poll(() => copied(page)).toEqual(['p.buf = append(p.buf, chunk...)\nreturn p.parse(lines)']);
        await expect(head.locator('.code-copy')).toHaveText('Copied');
        await expect(head.locator('.code-copy')).toHaveText('Copy', { timeout: 3_000 });
    });

    test('code header is not duplicated when the row re-renders', async ({ page }) => {
        await stubClipboard(page);
        await openSession(page, 'classic');
        await pushLog(page, 'text', ANSWER);

        const body = page.locator('.md-body', { hasText: 'Found it' });
        await expect(body.locator('.code-head')).toHaveCount(1, { timeout: 5_000 });
        // Markdown off and on again rebuilds the body from scratch.
        const md = page.locator('label:has-text("Markdown") input[type="checkbox"]');
        await md.uncheck();
        await expect(page.locator('.md-body')).toHaveCount(0);
        await md.check();
        await expect(body.locator('.code-head')).toHaveCount(1);
    });

    test('a refused clipboard shows the failure state', async ({ page }) => {
        await stubClipboard(page, true);
        await openSession(page, 'feed');
        await pushLog(page, 'text', ANSWER);

        const body = page.locator('.md-body', { hasText: 'Found it' });
        await expect(body).toBeVisible({ timeout: 5_000 });
        await body.hover();
        const btn = page.locator('[data-testid="entry-copy"]').last();
        await btn.click();
        await expect(btn).toHaveAttribute('title', /Copy failed/);
        await expect(page.locator('.md-body .code-copy').first()).toHaveText('Copy');
        await page.locator('.md-body .code-copy').first().click();
        await expect(page.locator('.md-body .code-copy').first()).toHaveText('Copy failed');
    });
});
