/**
 * App-wide right-click menu (ContextMenu.svelte + lib/contextMenu.ts).
 *
 * The clipboard and BrowserOpenURL are replaced by recorders in an init
 * script — what is under test is which text the menu hands over / inserts.
 */

import { test, expect } from './fixtures';
import type { Page } from '@playwright/test';

const SESSION_ID = 'test/S1';
const feedToggleSel = 'label:has-text("Feed") input[type="checkbox"]';
const URL = 'https://example.com/merge_requests/new?source_branch=feature%2FARB-818';

async function stubs(page: Page, clipboard = 'pasted text') {
    await page.addInitScript((clip) => {
        const w = window as typeof window & { __copied: string[]; __opened: string[]; runtime?: Record<string, unknown> };
        w.__copied = [];
        w.__opened = [];
        Object.defineProperty(navigator, 'clipboard', {
            configurable: true,
            value: {
                writeText: async (text: string) => { w.__copied.push(text); },
                readText: async () => clip,
            },
        });
        if (w.runtime) w.runtime.BrowserOpenURL = (u: string) => { w.__opened.push(u); };
    }, clipboard);
}

const copied = (page: Page) => page.evaluate(() => (window as typeof window & { __copied: string[] }).__copied);
const opened = (page: Page) => page.evaluate(() => (window as typeof window & { __opened: string[] }).__opened);

async function pushLog(page: Page, message: string) {
    await page.evaluate(
        ({ id, entry }) => {
            const w = window as typeof window & { __dispatchWailsEvent: (n: string, d: unknown) => void };
            w.__dispatchWailsEvent('session:log', { id, entry });
        },
        { id: SESSION_ID, entry: { time: new Date().toISOString(), level: 'text', source: 'claude', message } },
    );
}

async function openSession(page: Page) {
    await page.goto('/');
    const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
    await expect(row).toBeVisible({ timeout: 5_000 });
    await row.getByText('S1', { exact: true }).click();
    const toggle = page.locator(feedToggleSel);
    await expect(toggle).toBeVisible({ timeout: 5_000 });
    await toggle.check();
}

const menu = (page: Page) => page.locator('[data-testid="context-menu"]');
const item = (page: Page, action: string) => menu(page).locator(`[data-action="${action}"]`);

test.describe('Context menu', () => {
    test('link: copy address and open in the system browser', async ({ page }) => {
        await stubs(page);
        await openSession(page);
        await pushLog(page, `**Merge request** link:

- ${URL}`);

        const link = page.locator('.md-body a[href]', { hasText: 'example.com' }).last();
        await expect(link).toBeVisible({ timeout: 5_000 });
        await link.click({ button: 'right' });
        await expect(item(page, 'copyLink')).toBeVisible();
        await expect(item(page, 'paste')).toHaveCount(0);
        await item(page, 'copyLink').click();
        await expect(menu(page)).toHaveCount(0);
        await expect.poll(() => copied(page)).toEqual([URL]);

        await link.click({ button: 'right' });
        await item(page, 'openLink').click();
        await expect.poll(() => opened(page)).toEqual([URL]);
    });

    test('selected log text: Copy hands over the selection', async ({ page }) => {
        await stubs(page);
        await openSession(page);
        await pushLog(page, 'Branch pushed to origin as new');

        const p = page.getByText('Branch pushed to origin as new', { exact: true }).last();
        await expect(p).toBeVisible({ timeout: 5_000 });
        // No selection, no link, no code: nothing to offer -> no menu.
        await p.click({ button: 'right' });
        await expect(menu(page)).toHaveCount(0);

        await p.evaluate((el) => {
            const r = document.createRange();
            r.selectNodeContents(el);
            const s = window.getSelection()!;
            s.removeAllRanges();
            s.addRange(r);
        });
        await p.click({ button: 'right' });
        await item(page, 'copy').click();
        await expect.poll(() => copied(page)).toEqual(['Branch pushed to origin as new']);
    });

    test('code block: Copy code block', async ({ page }) => {
        await stubs(page);
        await openSession(page);
        await pushLog(page, 'Run:\n\n```sh\ngit push -u origin feature/ARB-818\n```');

        const pre = page.locator('.md-body pre', { hasText: 'git push' }).last();
        await expect(pre).toBeVisible({ timeout: 5_000 });
        await pre.click({ button: 'right' });
        await item(page, 'copyCode').click();
        await expect.poll(() => copied(page)).toEqual(['git push -u origin feature/ARB-818']);
    });

    test('text field: paste, select all, cut; Escape closes', async ({ page }) => {
        await stubs(page);
        await openSession(page);

        // The log filter box: always enabled, unlike the input of an idle session.
        const input = page.getByPlaceholder(/Filter log/);
        await expect(input).toBeVisible({ timeout: 5_000 });
        await input.fill('hello ');
        await input.evaluate((el: HTMLInputElement) => el.setSelectionRange(6, 6));

        await input.click({ button: 'right', position: { x: 5, y: 5 } });
        await expect(item(page, 'copy')).toHaveCount(0);
        await page.keyboard.press('Escape');
        await expect(menu(page)).toHaveCount(0);

        await input.evaluate((el: HTMLInputElement) => el.setSelectionRange(6, 6));
        await input.dispatchEvent('contextmenu', { button: 2 });
        await item(page, 'paste').click();
        await expect(input).toHaveValue('hello pasted text');

        await input.dispatchEvent('contextmenu', { button: 2 });
        await item(page, 'selectAll').click();
        await input.dispatchEvent('contextmenu', { button: 2 });
        await item(page, 'cut').click();
        await expect.poll(() => copied(page)).toEqual(['hello pasted text']);
        await expect(input).toHaveValue('');
    });
});
