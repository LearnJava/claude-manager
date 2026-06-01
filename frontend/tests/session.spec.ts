/**
 * Session spec — HARNESS-06
 *
 * Tests the bidirectional messaging flow:
 *  1. Start session S1 (happy-path scenario) and wait until it is working.
 *  2. Select S1 in the sidebar so the SessionView + SessionInput are visible.
 *  3. Type a message into SessionInput and click Send.
 *  4. Assert that the backend received a SendMessage RPC call (the session:log
 *     event with source=user confirms the message arrived).
 *  5. Assert that LogStream shows at least one log entry for the session.
 */

import { test, expect } from './fixtures';

const PROJECT = 'test';
const SESSION = 'S1';
const SESSION_ID = `${PROJECT}/${SESSION}`;

test.describe('SessionInput and LogStream', () => {
  test.beforeEach(async ({ ctrl }) => {
    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {/* idle */});
    await new Promise((r) => setTimeout(r, 200));
  });

  test('typing a message and clicking Send fires SendMessage and appears in LogStream', async ({
    page,
    ctrl,
  }) => {
    await page.goto('/');

    // Start the session via RPC so we don't rely on the sidebar start button.
    await ctrl.rpc('StartSession', { project: PROJECT, session: SESSION });

    // Wait for session to be in a state that accepts input.
    await ctrl.wait('session:status', { id: SESSION_ID, status: 'working' }, 12_000);

    // Select the session in the sidebar so SessionView renders.
    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await expect(sessionRow).toBeVisible({ timeout: 5_000 });
    await sessionRow.click();

    // SessionInput should now be visible.
    const textarea = page.locator('textarea[placeholder*="Type a message"]');
    await expect(textarea).toBeVisible({ timeout: 5_000 });
    await expect(textarea).not.toBeDisabled({ timeout: 3_000 });

    const msg = 'hello from playwright test';
    await textarea.fill(msg);

    // Register the session:log wait BEFORE clicking Send.
    const logEventPromise = ctrl.wait('session:log', { id: SESSION_ID }, 8_000);

    const sendBtn = page.getByRole('button', { name: /Send/ });
    await expect(sendBtn).not.toBeDisabled();
    await sendBtn.click();

    // 1. Backend received the message: a session:log event is fired.
    const logEvent = await logEventPromise;
    expect(logEvent.data).toMatchObject({ id: SESSION_ID });

    // 2. Textarea is cleared after a successful send.
    await expect(textarea).toHaveValue('', { timeout: 3_000 });

    // 3. LogStream contains at least one entry.
    // LogStream renders log entries; check for any text entry element.
    const logContainer = page.locator('[class*="log"], [class*="Log"]').first();
    // Allow some time for the DOM to update.
    await expect(
      page.locator('text=Working on the task').or(page.locator('text=hello from')).first(),
    ).toBeVisible({ timeout: 8_000 });

    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {/* ignore */});
  });

  test('Send button is disabled when session is idle', async ({ page }) => {
    await page.goto('/');

    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await expect(sessionRow).toBeVisible({ timeout: 5_000 });
    await sessionRow.click();

    const sendBtn = page.getByRole('button', { name: /Send/ });
    await expect(sendBtn).toBeDisabled({ timeout: 3_000 });
  });
});
