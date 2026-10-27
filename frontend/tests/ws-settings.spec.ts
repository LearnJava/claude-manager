/**
 * The WS feature switches are reachable from the GUI: Settings → Global
 * "Savings and control" turns them on without a restart, the project gates
 * and the nightly run sit on the Projects tab, strict gate rounds on the
 * Sessions tab, and the Experience panel's Quality tab shows reference cases,
 * quality labels and the benchmark (WASTE-TASKS.md WS-05..WS-12).
 */

import { test, expect } from './fixtures';

async function openSettings(page: import('@playwright/test').Page) {
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings' }).click();
  const modal = page.getByRole('dialog');
  await expect(modal).toBeVisible();
  return modal;
}

test('waiting screen switch shows the status-bar button right after Save', async ({ page }) => {
  const modal = await openSettings(page);
  await expect(modal.getByTestId('waste-flags-section')).toBeVisible();
  await expect(page.getByText(/Waiting for you: \d+/)).toHaveCount(0);

  const box = modal.getByTestId('waste-flag-waitingScreen');
  await box.check();
  await modal.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText(/Waiting for you: \d+/)).toBeVisible({ timeout: 5_000 });

  // Leave the shared config as it was for the rest of the suite.
  await box.uncheck();
  await modal.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText(/Waiting for you: \d+/)).toHaveCount(0, { timeout: 5_000 });
});

test('project gates and nightly run are on the Projects tab, gate rounds on Sessions', async ({ page }) => {
  const modal = await openSettings(page);
  await modal.getByRole('button', { name: 'Projects' }).click();
  await expect(modal.getByTestId('project-gates-0')).toBeVisible();
  await expect(modal.getByTestId('gate-schedule-0')).toBeVisible();
  await expect(modal.getByTestId('gate-schedule-queue-0')).toBeVisible();
  await modal.getByRole('button', { name: 'Sessions' }).click();
  await expect(modal.getByTestId('session-gate-rounds')).toBeVisible();
  // A missing locale key renders as the key itself.
  await expect(modal.getByText('Rework rounds on red gates (0 = off)')).toBeVisible();
  await expect(modal.getByText('settings.sessions', { exact: false })).toHaveCount(0);
});

test('Quality tab says how to turn on what is off', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Experience' }).click();
  const modal = page.getByRole('dialog');
  await modal.getByTestId('tab-quality').click();
  await expect(modal.getByTestId('quality-refs-off')).toBeVisible();
  await expect(modal.getByTestId('quality-labels-off')).toBeVisible();
  await expect(modal.getByTestId('quality-bench-off')).toBeVisible();
});
