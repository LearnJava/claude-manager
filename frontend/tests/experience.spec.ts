/**
 * Experience panel spec — LEARN-TASKS.md LN-03
 *
 * GetTopActions/GetActionSamples read straight from the app's own SQLite
 * store, which playwright-server never opens (cmd/playwright-server/main.go
 * passes SessionManager a nil store) — the same reason History.svelte and
 * CostDashboard.svelte have no real-data Playwright coverage (GUI-TESTS.md
 * HI-01, CD-01 are still "○"). helpers/bridge.ts stubs both calls to an
 * empty array, so this spec covers what's actually reachable here: the modal
 * opens/closes, the project/period pickers render and are interactive, and
 * an empty result renders the panel's own "no actions" empty state rather
 * than an unhandled RPC error.
 *
 * The Settings checkbox test is a real backend round-trip: toggling
 * "Experience layer" and saving does call UpdateConfig with
 * Optimization.ExperienceTracking set, verified via GetConfig — same pattern
 * as settings.spec.ts's session-field edit.
 */

import { test, expect } from './fixtures';

test.describe('Experience panel', () => {
  test('opens from the header, lists the configured project, and shows the empty state', async ({
    page,
  }) => {
    await page.goto('/');

    const experienceBtn = page.getByRole('button', { name: 'Experience' });
    await expect(experienceBtn).toBeVisible({ timeout: 5_000 });
    await experienceBtn.click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });
    await expect(modal).toContainText('Experience');
    await expect(modal.getByRole('button', { name: 'Actions' })).toBeVisible();

    // The "test" project (from the runtime config) is pre-selected.
    const projectSelect = modal.locator('select');
    await expect(projectSelect).toHaveValue('test', { timeout: 3_000 });

    // Period buttons are present and switchable.
    const period7 = modal.getByRole('button', { name: '7 days' });
    const period30 = modal.getByRole('button', { name: '30 days' });
    await expect(period30).toBeVisible();
    await period7.click();

    // GetTopActions is stubbed to return no rows — the panel's own empty
    // state renders instead of an error, mentioning how to turn the feature on.
    await expect(modal.getByText(/No recorded actions/i)).toBeVisible({ timeout: 5_000 });
    await expect(modal.getByText(/experience_tracking/)).toBeVisible();

    // The "✕" button closes the modal (same convention as History/CostDashboard).
    await modal.getByRole('button', { name: '✕' }).click();
    await expect(modal).not.toBeVisible({ timeout: 3_000 });
  });

  test('Settings → Global: toggling Experience layer persists through Save', async ({
    page,
    ctrl,
  }) => {
    await page.goto('/');

    const settingsBtn = page.getByRole('button', { name: 'Settings' });
    await expect(settingsBtn).toBeVisible({ timeout: 5_000 });
    await settingsBtn.click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });

    const checkbox = modal
        .getByText('Index finished runs into the Actions tab')
        .locator('input[type="checkbox"]');
    const wasChecked = await checkbox.isChecked();
    await checkbox.click();
    expect(await checkbox.isChecked()).toBe(!wasChecked);

    const saveBtn = modal.getByRole('button', { name: 'Save' });
    await saveBtn.click();
    await expect(modal.getByText(/Saved/i)).toBeVisible({ timeout: 5_000 });

    const cfg = await ctrl.rpc<{ Optimization?: { ExperienceTracking?: boolean } }>(
      'GetConfig',
      {},
    );
    expect(cfg.Optimization?.ExperienceTracking).toBe(!wasChecked);

    // Restore so other tests are not affected by the flip.
    await checkbox.click();
    await saveBtn.click();
    await expect(modal.getByText(/Saved/i)).toBeVisible({ timeout: 5_000 });

    await modal.getByRole('button', { name: 'Close' }).click();
    await expect(modal).not.toBeVisible({ timeout: 3_000 });
  });
});
