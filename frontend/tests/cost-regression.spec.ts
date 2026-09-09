/**
 * Cost-regression alert spec — LEARN-TASKS.md LN-16.
 *
 * DetectRegression itself is pure backend logic (internal/experience/
 * regression_test.go covers growth/noise/insufficient-history). What's
 * under test here is purely the DOM: an `experience:regression` event
 * (dispatched the same way the real manager.finishRun emits it) drives
 * StatusBar's counter and CostDashboard's dismissible banner — no fakeclaude
 * scenario needed, since neither depends on a session lifecycle.
 */

import { test, expect } from './fixtures';
import type { Page } from '@playwright/test';

async function emitRegression(
    page: Page,
    overrides: Partial<{ project: string; session: string; run_id: number; factor: number; hint: string }> = {},
) {
    await page.evaluate((evt) => {
        const w = window as typeof window & {
            __dispatchWailsEvent: (n: string, d: unknown) => void;
        };
        w.__dispatchWailsEvent('experience:regression', evt);
    }, {
        project: 'test',
        session: 'S1',
        run_id: 42,
        factor: 2.4,
        hint: 'manager prompt overhead grew: primer 100→400 chars',
        ...overrides,
    });
}

// waitReady blocks until the app's async bootstrap (initProjects +
// initSessions, including the EventsOn subscriptions the tests below rely
// on) has had a chance to run — mirrors the "wait for a stable header
// button" readiness check every other spec in this suite uses before its
// first interaction (see experience.spec.ts, sidebar.spec.ts).
async function waitReady(page: Page) {
    const dashboardBtn = page.getByRole('button', { name: 'Dashboard' });
    await expect(dashboardBtn).toBeVisible({ timeout: 5_000 });
}

test.describe('Cost-regression alerts', () => {
    test('StatusBar counter appears and opens the dashboard (STB-08)', async ({ page }) => {
        await page.goto('/');
        await waitReady(page);

        const counter = page.getByRole('button', { name: /Regressions:/ });
        await expect(counter).not.toBeVisible();

        await emitRegression(page);
        await expect(counter).toBeVisible({ timeout: 3_000 });
        await expect(counter).toContainText('Regressions: 1');

        await counter.click();
        const modal = page.getByRole('dialog');
        await expect(modal).toBeVisible({ timeout: 3_000 });
        await expect(modal).toContainText('Usage Dashboard');
    });

    test('CostDashboard banner shows project/session/factor/hint and dismisses without a re-fetch (CD-12)', async ({ page }) => {
        await page.goto('/');
        await waitReady(page);
        await emitRegression(page);

        await page.getByRole('button', { name: 'Dashboard' }).click();
        const modal = page.getByRole('dialog');
        await expect(modal).toBeVisible({ timeout: 3_000 });

        const banner = modal.locator('text=test/S1');
        await expect(banner).toBeVisible();
        await expect(modal).toContainText('2.4x');
        await expect(modal).toContainText('primer 100→400 chars');

        // Dismiss — the banner disappears, the modal (and its own KPI data)
        // stays open, and the StatusBar counter drops back to 0.
        await modal.getByRole('button', { name: '✕' }).last().click();
        await expect(modal.locator('text=test/S1')).not.toBeVisible();
        await expect(modal).toBeVisible();

        await modal.getByRole('button', { name: '✕' }).first().click();
        await expect(modal).not.toBeVisible({ timeout: 3_000 });
        await expect(page.getByRole('button', { name: /Regressions:/ })).not.toBeVisible();
    });

    test('multiple regressions stack and dismiss independently', async ({ page }) => {
        await page.goto('/');
        await waitReady(page);
        await emitRegression(page, { session: 'S1', run_id: 1 });
        await emitRegression(page, { session: 'S2', run_id: 2 });

        const counter = page.getByRole('button', { name: /Regressions:/ });
        await expect(counter).toContainText('Regressions: 2');

        await page.getByRole('button', { name: 'Dashboard' }).click();
        const modal = page.getByRole('dialog');
        await expect(modal.locator('text=test/S1')).toBeVisible();
        await expect(modal.locator('text=test/S2')).toBeVisible();

        // ✕ buttons in DOM order: [0] the modal's own header close, [1] the
        // first (S1) alert, [2] the second (S2) alert — dismissing index 1
        // must leave S2's alert (and the modal itself) untouched.
        await modal.getByRole('button', { name: '✕' }).nth(1).click();
        await expect(modal.locator('text=test/S1')).not.toBeVisible();
        await expect(modal.locator('text=test/S2')).toBeVisible();
        await expect(counter).toContainText('Regressions: 1');
    });
});
