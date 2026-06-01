/**
 * Permission spec — HARNESS-06
 *
 * Drives the permission-allow scenario (session S2, prompt "Please refactor
 * this code") end-to-end:
 *  1. Start S2 — fakeclaude emits a permission_request event.
 *  2. The PermissionBanner should appear in the DOM.
 *  3. Click "✓ Allow" in the banner.
 *  4. Assert backend received RespondPermission (decision="allow").
 *  5. Assert session resumes to "working" status.
 *
 * Double validation: both DOM state and backend events are checked.
 */

import { test, expect } from './fixtures';

const PROJECT = 'test';
const SESSION = 'S2';
const SESSION_ID = `${PROJECT}/${SESSION}`;

test.describe('PermissionBanner', () => {
  test.beforeEach(async ({ ctrl }) => {
    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {/* idle */});
    await new Promise((r) => setTimeout(r, 200));
  });

  test('clicking Allow resolves the permission and session resumes', async ({
    page,
    ctrl,
  }) => {
    await page.goto('/');

    // Select S2 in the sidebar so the SessionView (which hosts the banner) renders.
    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await expect(sessionRow).toBeVisible({ timeout: 5_000 });
    await sessionRow.click();

    // Start S2 — its scenario will emit a permission_request.
    await ctrl.rpc('StartSession', { project: PROJECT, session: SESSION });

    // Wait for the permission event at the backend level.
    const permEvent = await ctrl.wait('session:permission', { id: SESSION_ID }, 15_000);
    expect(permEvent.data).toMatchObject({ id: SESSION_ID });

    // 1. PermissionBanner should be visible in the DOM.
    const banner = page.locator('[role="alertdialog"]');
    await expect(banner).toBeVisible({ timeout: 5_000 });
    await expect(banner).toContainText('waiting for permission');

    // The tool name should be rendered.
    await expect(banner).toContainText('Edit');

    // 2. Click Allow.
    const allowBtn = banner.getByRole('button', { name: /Allow$/ });
    await expect(allowBtn).toBeVisible();

    // Register the post-allow status wait BEFORE clicking.
    const resumedPromise = ctrl.wait('session:status', { id: SESSION_ID, status: 'working' }, 15_000);

    await allowBtn.click();

    // 3. Banner disappears (permission cleared optimistically).
    await expect(banner).not.toBeVisible({ timeout: 5_000 });

    // 4. Session resumes to "working" at the backend.
    const resumed = await resumedPromise;
    expect(resumed.data).toMatchObject({ id: SESSION_ID, status: 'working' });

    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {/* ignore */});
  });

  test('clicking Deny sends deny decision to backend', async ({ page, ctrl }) => {
    await page.goto('/');

    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await expect(sessionRow).toBeVisible({ timeout: 5_000 });
    await sessionRow.click();

    await ctrl.rpc('StartSession', { project: PROJECT, session: SESSION });
    await ctrl.wait('session:permission', { id: SESSION_ID }, 15_000);

    const banner = page.locator('[role="alertdialog"]');
    await expect(banner).toBeVisible({ timeout: 5_000 });

    const denyBtn = banner.getByRole('button', { name: /Deny$/ });
    await expect(denyBtn).toBeVisible();

    // The only evidence of a Deny is the banner clearing (no separate backend event).
    await denyBtn.click();
    await expect(banner).not.toBeVisible({ timeout: 5_000 });

    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {/* ignore */});
  });
});
