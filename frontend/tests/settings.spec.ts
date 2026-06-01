/**
 * Settings spec — HARNESS-06
 *
 * Opens the Settings modal, switches between tabs, edits a SessionConfig field,
 * saves, and verifies:
 *  1. The "Sessions" tab renders the session editor.
 *  2. Editing the "Model" select is reflected in the Save payload.
 *  3. The UpdateConfig RPC is called with the modified config.
 *  4. A subsequent GetConfig confirms the value is persisted.
 */

import { test, expect } from './fixtures';

test.describe('Settings modal', () => {
  test('can open, switch tabs, edit a session field, and Save triggers UpdateConfig', async ({
    page,
    ctrl,
  }) => {
    await page.goto('/');

    // Open Settings via the header button.
    const settingsBtn = page.getByRole('button', { name: 'Settings' });
    await expect(settingsBtn).toBeVisible({ timeout: 5_000 });
    await settingsBtn.click();

    // The modal should appear.
    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });
    await expect(modal).toContainText('Settings');

    // ── Global tab (default) ──────────────────────────────────────────────────
    const globalTab = modal.getByRole('button', { name: 'Global' });
    await expect(globalTab).toBeVisible();
    // It should already be active; check for a field that only exists there.
    await expect(modal.getByText('Claude CLI path')).toBeVisible();

    // ── Projects tab ──────────────────────────────────────────────────────────
    const projectsTab = modal.getByRole('button', { name: 'Projects' });
    await projectsTab.click();
    await expect(modal.getByText(/project.*configured/i)).toBeVisible({ timeout: 3_000 });

    // ── Sessions tab ──────────────────────────────────────────────────────────
    const sessionsTab = modal.getByRole('button', { name: 'Sessions' });
    await sessionsTab.click();

    // Select session S1.
    const sessionListItem = modal.getByRole('button', { name: 'S1' }).first();
    await expect(sessionListItem).toBeVisible({ timeout: 3_000 });
    await sessionListItem.click();

    // The session editor should show the Model select.
    const modelSelect = modal.locator('label', { hasText: 'Model' }).locator('select').first();
    await expect(modelSelect).toBeVisible({ timeout: 3_000 });

    // Change model to "haiku".
    await modelSelect.selectOption('haiku');
    await expect(modelSelect).toHaveValue('haiku');

    // Save.
    const saveBtn = modal.getByRole('button', { name: 'Save' });
    await expect(saveBtn).not.toBeDisabled();
    await saveBtn.click();

    // Wait for the "Saved." info message to appear.
    await expect(modal.getByText('Saved.')).toBeVisible({ timeout: 5_000 });

    // ── Backend verification ──────────────────────────────────────────────────
    // GetConfig should now report S1 model = haiku.
    const cfg = await ctrl.rpc<{
      Projects?: Array<{ Name: string; Sessions?: Array<{ Name: string; Model: string }> }>;
    }>('GetConfig', {});

    const testProject = cfg.Projects?.find((p) => p.Name === 'test');
    expect(testProject).toBeDefined();
    const s1 = testProject?.Sessions?.find((s) => s.Name === 'S1');
    expect(s1?.Model).toBe('haiku');

    // Restore original value so other tests are not affected.
    if (s1) {
      s1.Model = 'claude-sonnet-4-6';
      const payload = { ...cfg, Projects: cfg.Projects };
      await ctrl.rpc('UpdateConfig', { config: payload });
    }

    // Close.
    const closeBtn = modal.getByRole('button', { name: 'Close' });
    await closeBtn.click();
    await expect(modal).not.toBeVisible({ timeout: 3_000 });
  });
});
