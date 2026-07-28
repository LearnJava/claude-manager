/**
 * Resume-prompt spec.
 *
 * An interrupted session (stopped / crashed / app killed mid-task) leaves a
 * crash-recovery state file carrying its CLI session id — see CLAUDE.md
 * "Crash Recovery". Clicking ▶ on such a session must ask whether to continue
 * that conversation or start from scratch, instead of silently resuming.
 *
 * The Playwright config runs with crash_recovery = false (no state files are
 * written at all), so the state is injected by overriding GetSessionState on the
 * bridge instead of seeding a file: what is under test here is the Sidebar
 * wiring (prompt shown, correct calls per button), while the state-file
 * semantics themselves are covered by the Go tests in internal/session.
 */

import { test, expect } from './fixtures';

const PROJECT = 'test';
const SESSION = 'S1';
const SESSION_ID = `${PROJECT}/${SESSION}`;

/** Stub GetSessionState to report an unfinished run and record the calls made. */
async function stubUnfinished(page: import('@playwright/test').Page) {
  // Runs after the bridge's init script, so window.go.main.App already exists.
  await page.addInitScript(() => {
    const w = window as any;
    w.__calls = { clear: [] as string[], start: [] as string[] };
    const App = w.go.main.App;
    App.GetSessionState = () =>
      Promise.resolve({
        session_id: 'crashed-cli-session-xyz',
        started_at: new Date(Date.now() - 3_600_000).toISOString(),
        task: 'ROADMAP.md:92 | fix layout reflow',
      });
    App.ClearSessionState = (p: string, n: string) => {
      // Record only: nothing was seeded on disk to clear.
      w.__calls.clear.push(`${p}/${n}`);
      return Promise.resolve();
    };
    const origStart = App.StartSession;
    App.StartSession = (p: string, n: string) => {
      w.__calls.start.push(`${p}/${n}`);
      return origStart(p, n);
    };
  });
}

test.describe('Resume prompt', () => {
  test.beforeEach(async ({ ctrl }) => {
    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {/* already idle */});
    await new Promise((r) => setTimeout(r, 200));
  });

  test.afterEach(async ({ ctrl }) => {
    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {/* ignore */});
  });

  test('Start on an unfinished session asks before resuming', async ({ page }) => {
    await stubUnfinished(page);
    await page.goto('/');

    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await expect(sessionRow).toBeVisible({ timeout: 5_000 });

    // The row is marked as having unfinished work.
    await expect(page.getByTestId(`unfinished-${SESSION_ID}`)).toBeVisible({ timeout: 5_000 });

    await sessionRow.hover();
    await sessionRow.getByTitle('Start', { exact: true }).click();

    // The prompt appears with the interrupted task, and nothing has started yet.
    await expect(page.getByText('Unfinished session')).toBeVisible({ timeout: 3_000 });
    await expect(page.getByTestId('resume-task')).toHaveText(/fix layout reflow/);
    expect(await page.evaluate(() => (window as any).__calls.start)).toEqual([]);
  });

  test('Continue starts the session without clearing the saved state', async ({ page }) => {
    await stubUnfinished(page);
    await page.goto('/');

    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await sessionRow.hover();
    await sessionRow.getByTitle('Start', { exact: true }).click();
    await page.getByRole('button', { name: 'Continue' }).click();

    await expect
      .poll(() => page.evaluate(() => (window as any).__calls.start), { timeout: 5_000 })
      .toEqual([SESSION_ID]);
    expect(await page.evaluate(() => (window as any).__calls.clear)).toEqual([]);
  });

  test('Start fresh clears the saved state before starting', async ({ page }) => {
    await stubUnfinished(page);
    await page.goto('/');

    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await sessionRow.hover();
    await sessionRow.getByTitle('Start', { exact: true }).click();
    await page.getByRole('button', { name: 'Start fresh' }).click();

    await expect
      .poll(() => page.evaluate(() => (window as any).__calls.clear), { timeout: 5_000 })
      .toEqual([SESSION_ID]);
    expect(await page.evaluate(() => (window as any).__calls.start)).toEqual([SESSION_ID]);
  });

  test('Cancel starts nothing', async ({ page }) => {
    await stubUnfinished(page);
    await page.goto('/');

    const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
    await sessionRow.hover();
    await sessionRow.getByTitle('Start', { exact: true }).click();
    await expect(page.getByText('Unfinished session')).toBeVisible({ timeout: 3_000 });
    await page.getByRole('button', { name: 'Cancel' }).click();

    await expect(page.getByText('Unfinished session')).toHaveCount(0);
    expect(await page.evaluate(() => (window as any).__calls.start)).toEqual([]);
    expect(await page.evaluate(() => (window as any).__calls.clear)).toEqual([]);
  });
});
