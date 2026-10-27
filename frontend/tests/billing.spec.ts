/**
 * Billing spec — WASTE-TASKS.md WS-02.
 *
 * On a subscription the scarce things are the rate-limit window and time, not
 * dollars: the readouts lead with tokens taken out of the limit window and with
 * time. In 'api' mode they behave exactly as before. The first half pins the
 * pure helpers (src/lib/billing.ts) — the same numbers feed both modes, only
 * order and labels differ; the second drives the real UI in both modes.
 */

import { test, expect } from './fixtures';
import {
  limitTokens,
  windowShare,
  formatWindowShare,
  readoutOrder,
  normaliseBilling,
} from '../src/lib/billing';

const RUN = {
  input_tokens: 1000,
  output_tokens: 200,
  cache_read: 50_000,
  cache_creation: 3000,
};

test.describe('billing helpers', () => {
  test('limitTokens is input + cache write + cache read, output left out', () => {
    expect(limitTokens(RUN)).toBe(54_000);
    // Same number from the Go-exported run-row shape.
    expect(
      limitTokens({ InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 50_000, CacheCreationTokens: 3000 }),
    ).toBe(54_000);
    expect(limitTokens(undefined)).toBe(0);
  });

  test('an unknown window is null, never 0%, and never divides by zero', () => {
    expect(windowShare(54_000, 0)).toBeNull();
    expect(windowShare(54_000, undefined)).toBeNull();
    expect(windowShare(54_000, -5)).toBeNull();
    expect(windowShare(54_000, NaN)).toBeNull();
    expect(formatWindowShare(null, 'share unknown')).toBe('share unknown');
  });

  test('a known window gives a fraction; zero spend is a real 0%', () => {
    expect(windowShare(500_000, 2_000_000)).toBeCloseTo(0.25);
    expect(windowShare(0, 2_000_000)).toBe(0);
    expect(formatWindowShare(0, 'x')).toBe('0%');
    expect(formatWindowShare(0.25, 'x')).toBe('25%');
    // A tiny non-zero spend must not read as 0%.
    expect(formatWindowShare(0.002, 'x')).toBe('<1%');
    // Over one window is honest, not capped.
    expect(formatWindowShare(1.3, 'x')).toBe('130%');
  });

  test('mode changes the order of readouts, not the data', () => {
    expect(readoutOrder('subscription', 'tokens')).toEqual(['window', 'time', 'usd']);
    // The old dollars/tokens switch is ignored on a subscription...
    expect(readoutOrder('subscription', 'usd')).toEqual(['window', 'time', 'usd']);
    // ...and is exactly the old behaviour in api mode.
    expect(readoutOrder('api', 'tokens')).toEqual(['tokens', 'usd']);
    expect(readoutOrder('api', 'usd')).toEqual(['usd', 'tokens']);
    // The numbers feeding both are untouched by the mode.
    const before = limitTokens(RUN);
    readoutOrder('api', 'usd');
    expect(limitTokens(RUN)).toBe(before);
  });

  test('anything but "api" is subscription', () => {
    expect(normaliseBilling('api')).toBe('api');
    expect(normaliseBilling('subscription')).toBe('subscription');
    expect(normaliseBilling('')).toBe('subscription');
    expect(normaliseBilling(undefined)).toBe('subscription');
    expect(normaliseBilling('free')).toBe('subscription');
  });
});

// Wraps GetConfig so the app boots with the given billing settings, whatever
// the shared test server has configured.
async function bootWithBilling(
  page: import('@playwright/test').Page,
  billing: string,
  windowTokens: number,
) {
  await page.addInitScript(
    ({ b, w }) => {
      const app = (window as any).go.main.App;
      const orig = app.GetConfig;
      app.GetConfig = () =>
        orig().then((c: any) => ({ ...c, Settings: { ...c.Settings, Billing: b, LimitWindowTokens: w } }));
      localStorage.removeItem('cm.billing');
    },
    { b: billing, w: windowTokens },
  );
}

async function openSessionCard(page: import('@playwright/test').Page) {
  await page.goto('/');
  const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
  await expect(row).toBeVisible({ timeout: 5_000 });
  await row.click();
}

test.describe('billing UI', () => {
  test('subscription: card and status bar lead with the window and time; unknown window says so', async ({
    page,
  }) => {
    await bootWithBilling(page, 'subscription', 0);
    await openSessionCard(page);

    await expect(page.getByTestId('card-limit')).toBeVisible({ timeout: 5_000 });
    await expect(page.getByTestId('card-limit-share')).toContainText('share unknown');
    await expect(page.getByTestId('card-limit-share')).not.toContainText('0%');
    await expect(page.getByTestId('card-time')).toBeVisible();
    await expect(page.getByTestId('card-cost-conventional')).toContainText('conv. units');

    await expect(page.getByTestId('today-subscription')).toBeVisible();
    await expect(page.getByTestId('today-share')).toContainText('share unknown');
    await expect(page.getByTestId('today-time')).toBeVisible();
  });

  test('subscription with a known window shows a percentage instead', async ({ page }) => {
    await bootWithBilling(page, 'subscription', 5_000_000);
    await openSessionCard(page);

    await expect(page.getByTestId('card-limit-share')).toContainText('of window', { timeout: 5_000 });
    await expect(page.getByTestId('card-limit-share')).not.toContainText('unknown');
  });

  test('api mode keeps the old readouts', async ({ page }) => {
    await bootWithBilling(page, 'api', 5_000_000);
    await openSessionCard(page);

    await expect(page.getByText('Cost:').first()).toBeVisible({ timeout: 5_000 });
    await expect(page.getByTestId('card-limit')).toHaveCount(0);
    await expect(page.getByTestId('card-time')).toHaveCount(0);
    await expect(page.getByTestId('today-subscription')).toHaveCount(0);
    // The status bar's old unit switch is still there.
    await expect(page.getByRole('button', { name: /Today:/ })).toBeVisible();
  });

  test('dashboard headline: window and time on a subscription, total amount in api mode', async ({ page }) => {
    await bootWithBilling(page, 'subscription', 0);
    await page.goto('/');
    await page.getByRole('button', { name: 'Dashboard' }).first().click();
    const modal = page.getByRole('dialog');
    await expect(modal.getByTestId('dash-limit')).toBeVisible({ timeout: 5_000 });
    await expect(modal.getByTestId('dash-limit-share')).toContainText('share unknown');
    await expect(modal.getByTestId('dash-time')).toBeVisible();
  });

  test('dashboard in api mode has neither the window nor the time card', async ({ page }) => {
    await bootWithBilling(page, 'api', 0);
    await page.goto('/');
    await page.getByRole('button', { name: 'Dashboard' }).first().click();
    const modal = page.getByRole('dialog');
    await expect(modal).toContainText('Usage Dashboard', { timeout: 5_000 });
    await expect(modal.getByTestId('dash-limit')).toHaveCount(0);
    await expect(modal.getByTestId('dash-time')).toHaveCount(0);
  });
});
