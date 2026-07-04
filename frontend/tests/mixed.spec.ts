/**
 * Mixed programming spec — MP-08
 *
 * Two flows, both double-checked against the control-plane:
 *
 *  1. Settings → Workers tab: adding a preset worker renders the editor and the
 *     saved config carries the worker through UpdateConfig (regression guard for
 *     normaliseConfig dropping [[worker]] on save).
 *
 *  2. Mixed modal dispatch: entering a brief and clicking Dispatch drives a full
 *     round against the fakeworker; the UI shows the completed task timeline and
 *     the quality table, and the backend emits worker:done with status=done.
 *     Skipped when git is unavailable (CM_MIXED_ENABLED=0).
 */

import { test, expect } from './fixtures';

const mixedEnabled = process.env.CM_MIXED_ENABLED !== '0';

test.describe('Mixed programming', () => {
  test('Workers tab: adding a preset persists the worker through Save', async ({ page, ctrl }) => {
    await page.goto('/');

    const settingsBtn = page.getByRole('button', { name: 'Settings' });
    await expect(settingsBtn).toBeVisible({ timeout: 5_000 });
    await settingsBtn.click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });

    await modal.getByRole('button', { name: 'Workers' }).click();

    // Add the Step 3.7 preset.
    await modal.getByRole('button', { name: /Step 3\.7 Flash/ }).click();

    // The editor renders and shows the preset name.
    const editor = modal.locator('[data-testid="worker-editor"]');
    await expect(editor).toBeVisible({ timeout: 3_000 });
    const nameInput = editor.locator('input').first();
    await expect(nameInput).toHaveValue('step37');

    // Save and confirm.
    const saveBtn = modal.getByRole('button', { name: 'Save' });
    await expect(saveBtn).not.toBeDisabled();
    await saveBtn.click();
    await expect(modal.getByText('Saved.')).toBeVisible({ timeout: 5_000 });

    // Backend verification: GetConfig reports the worker.
    const cfg = await ctrl.rpc<{ Workers?: Array<{ Name: string; Model: string }> }>('GetConfig', {});
    const step37 = cfg.Workers?.find((w) => w.Name === 'step37');
    expect(step37).toBeDefined();
    expect(step37?.Model).toBe('stepfun/step-3.7-flash:free');

    // Restore config so other tests are not affected by the added worker.
    const restored = { ...cfg, Workers: (cfg.Workers ?? []).filter((w) => w.Name !== 'step37') };
    await ctrl.rpc('UpdateConfig', { config: restored });

    await modal.getByRole('button', { name: 'Close' }).click();
    await expect(modal).not.toBeVisible({ timeout: 3_000 });
  });

  test('Mixed modal: dispatching a brief runs a round and renders the timeline', async ({
    page,
    ctrl,
  }) => {
    test.skip(!mixedEnabled, 'git not available — mixed project not configured');

    await page.goto('/');

    // Open the Mixed modal from the header.
    const mixedBtn = page.getByRole('button', { name: 'Mixed', exact: true });
    await expect(mixedBtn).toBeVisible({ timeout: 5_000 });
    await mixedBtn.click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });
    await expect(modal).toContainText('Mixed programming');

    // The mixed project + fake worker should be pre-selected.
    await expect(modal.locator('[data-testid="mixed-project"]')).toHaveValue('mixedproj', {
      timeout: 3_000,
    });
    await expect(modal.locator('[data-testid="mixed-worker"]')).toHaveValue('fake');

    // Enter a brief that matches the seeded greet.go anchor.
    await modal
      .locator('[data-testid="mixed-brief"]')
      .fill('Implement Greet: replace the TODO return with a real greeting.');

    // Dispatch and wait for the backend to finish the task. The dispatch RPC
    // blocks until done, so also watch for the worker:done event concurrently.
    const [doneEvent] = await Promise.all([
      ctrl.wait('worker:done', { status: 'done' }, 30_000),
      modal.locator('[data-testid="mixed-dispatch"]').click(),
    ]);
    expect((doneEvent.data as { rounds: number }).rounds).toBe(1);

    // The tasks list shows the completed task and the quality table populates.
    const tasks = modal.locator('[data-testid="mixed-tasks"]');
    await expect(tasks).toBeVisible({ timeout: 10_000 });
    await expect(tasks).toContainText('done');
    await expect(tasks).toContainText('Round 1');

    const quality = modal.locator('[data-testid="mixed-quality"]');
    await expect(quality).toBeVisible();
    await expect(quality).toContainText('fake');

    await modal.getByRole('button', { name: 'Close' }).click();
    await expect(modal).not.toBeVisible({ timeout: 3_000 });
  });
});
