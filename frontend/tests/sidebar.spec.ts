/**
 * Sidebar spec — HARNESS-06
 *
 * Clicks the ▶ (Start) button for session S1 in the "test" project group and
 * asserts two things in parallel:
 *  1. DOM: the status dot for S1 eventually turns the "working" colour class.
 *  2. Backend: the control-plane emits a session:status event with status=working.
 *
 * This double check catches a "button clicked but method never called" desync
 * between the UI and the Go backend.
 */

import { test, expect } from './fixtures';

const PROJECT = 'test';
const SESSION = 'S1';
const SESSION_ID = `${PROJECT}/${SESSION}`;

test.describe('Sidebar', () => {
  test.beforeEach(async ({ ctrl }) => {
    // Ensure S1 is idle before each test.
    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {/* already idle */});
    // Give session manager a moment to settle.
    await new Promise((r) => setTimeout(r, 200));
  });

  test('clicking Start turns status dot green and fires session:status event', async ({
    page,
    ctrl,
  }) => {
    await page.goto('/');

    // The sidebar should render the "test" project group.
    const projectHeader = page.getByRole('button', { name: /test/i }).first();
    await expect(projectHeader).toBeVisible({ timeout: 5_000 });

    // Expand the group if collapsed.
    const arrowText = await projectHeader.locator('..').locator('span').first().textContent();
    if (arrowText?.trim() === '▶') {
      await projectHeader.click();
    }

    // Find the ▶ toggle button for session S1.
    // In the sidebar each session row has a group-hover button with title "Start".
    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await expect(sessionRow).toBeVisible({ timeout: 3_000 });

    // Hover to reveal the toggle button, then click it.
    await sessionRow.hover();
    const startBtn = sessionRow.getByTitle('Start', { exact: true });
    await expect(startBtn).toBeVisible({ timeout: 2_000 });

    // Kick off both assertions concurrently — the backend event and the DOM
    // update are caused by the same click.
    const [statusEvent] = await Promise.all([
      ctrl.wait('session:status', { id: SESSION_ID, status: 'working' }, 12_000),
      startBtn.click(),
    ]);

    // 1. Backend emitted the correct event.
    expect(statusEvent.data).toMatchObject({ id: SESSION_ID, status: 'working' });

    // 2. DOM: the status dot for S1 uses bg-status-working.
    const dot = sessionRow.locator('.bg-status-working, .rounded-full');
    await expect(dot.first()).toHaveClass(/bg-status-working/, { timeout: 5_000 });

    // Clean up.
    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {/* ignore */});
  });

  // The model dropdown used to mix two vocabularies: our CLI aliases
  // (haiku/sonnet/opus) plus whatever spelling the session's model happened to
  // have — S1 is configured as the pinned id "claude-sonnet-4-6", which was
  // appended as a fifth option next to "sonnet". Both must now collapse onto
  // one entry (see frontend/src/lib/models.ts).
  test('model dropdown offers one entry per model, no duplicate spellings', async ({ page }) => {
    await page.goto('/');

    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await expect(sessionRow).toBeVisible({ timeout: 5_000 });

    const modelSelect = sessionRow.locator('select');
    await expect(modelSelect).toHaveValue('sonnet', { timeout: 3_000 });
    await expect(modelSelect.locator('option')).toHaveText(['Haiku', 'Sonnet', 'Opus', 'Fable']);
  });

  test('status dot is idle (bg-status-idle) before session starts', async ({ page }) => {
    await page.goto('/');

    const projectHeader = page.getByRole('button', { name: /test/i }).first();
    await expect(projectHeader).toBeVisible({ timeout: 5_000 });

    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await expect(sessionRow).toBeVisible({ timeout: 3_000 });

    // The dot should be the idle colour.
    const dot = sessionRow.locator('.rounded-full').first();
    await expect(dot).toHaveClass(/bg-status-idle/, { timeout: 3_000 });
  });
});
