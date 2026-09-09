/**
 * Experience panel spec — LEARN-TASKS.md LN-03/LN-04/LN-18
 *
 * GetTopActions/GetActionSamples/GetPermissionCandidates/GetDurationProfile
 * read straight from the app's own SQLite store, which playwright-server
 * never opens (cmd/playwright-server/main.go passes SessionManager a nil
 * store) — the same reason History.svelte and CostDashboard.svelte have no
 * real-data Playwright coverage (GUI-TESTS.md HI-01, CD-01 are still "○").
 * helpers/bridge.ts stubs all four to an empty result, so this spec covers
 * what's actually reachable here: the modal opens/closes, the tabs switch,
 * the project/period pickers render and are interactive, and an empty result
 * renders each tab's own empty state rather than an unhandled RPC error.
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

  test('Permissions tab: empty state, then a suggestion list with a working Add-rule button', async ({
    page,
  }) => {
    await page.goto('/');

    const experienceBtn = page.getByRole('button', { name: 'Experience' });
    await expect(experienceBtn).toBeVisible({ timeout: 5_000 });
    await experienceBtn.click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });

    await modal.getByRole('button', { name: 'Permissions' }).click();

    // GetPermissionCandidates is stubbed empty by default — the tab's own
    // empty state renders instead of an error.
    await expect(modal.getByText(/No permission candidates/i)).toBeVisible({ timeout: 5_000 });

    // Override the stub to return one safe candidate and record AddPermissionRule
    // calls, then reload the panel via Refresh to pick it up.
    await page.evaluate(() => {
      const w = window as any;
      w.__addRuleCalls = [] as unknown[];
      const App = w.go.main.App;
      App.GetPermissionCandidates = () =>
        Promise.resolve({
          Safe: [
            {
              Tool: 'Bash',
              Pattern: 'go test ./...',
              Count: 5,
              AllowCount: 5,
              DenyCount: 0,
              Safe: true,
              FirstSeen: new Date().toISOString(),
              LastSeen: new Date().toISOString(),
            },
          ],
          NeedsReview: [],
        });
      App.AddPermissionRule = (...args: unknown[]) => {
        w.__addRuleCalls.push(args);
        return Promise.resolve(undefined);
      };
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByText('go test ./...')).toBeVisible({ timeout: 5_000 });
    await expect(modal.getByText('Safe to auto-allow (1)')).toBeVisible();

    // Whichever session the row's picker defaults to (the project's first
    // configured session) is what "Add rule" must target.
    const sessionPicked = await modal.locator('table').first().locator('select').inputValue();
    await modal.getByRole('button', { name: 'Add rule' }).click();
    await expect(modal.getByText('Added')).toBeVisible({ timeout: 3_000 });

    const calls = await page.evaluate(() => (window as any).__addRuleCalls);
    expect(calls).toEqual([['test', sessionPicked, 'Bash', 'go test ./...', 'allow']]);
  });

  test('Timing tab: empty state, then a populated duration profile table', async ({
    page,
  }) => {
    await page.goto('/');

    const experienceBtn = page.getByRole('button', { name: 'Experience' });
    await expect(experienceBtn).toBeVisible({ timeout: 5_000 });
    await experienceBtn.click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });

    await modal.getByRole('button', { name: 'Timing' }).click();

    // GetDurationProfile is stubbed empty by default — the tab's own empty
    // state renders instead of an error.
    await expect(modal.getByText(/No duration profile/i)).toBeVisible({ timeout: 5_000 });

    // Override the stub to return one signature, then reload via Refresh.
    await page.evaluate(() => {
      const w = window as any;
      w.go.main.App.GetDurationProfile = () =>
        Promise.resolve([
          {
            Sig: 'Bash:cargo test --release',
            Tool: 'Bash',
            Count: 12,
            MedianSec: 130,
            P90Sec: 200,
            MaxSec: 240,
            TotalSec: 1600,
            FailRate: 0.1,
          },
        ]);
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByText('Bash:cargo test --release')).toBeVisible({ timeout: 5_000 });
    // formatDuration(130_000) -> "2m 10s".
    await expect(modal.getByText('2m 10s')).toBeVisible();

    await modal.getByRole('button', { name: '✕' }).click();
    await expect(modal).not.toBeVisible({ timeout: 3_000 });
  });

  test('Cost by tool tab: empty state, then a populated attribution report', async ({
    page,
  }) => {
    await page.goto('/');

    const experienceBtn = page.getByRole('button', { name: 'Experience' });
    await expect(experienceBtn).toBeVisible({ timeout: 5_000 });
    await experienceBtn.click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });

    await modal.getByRole('button', { name: 'Cost by tool' }).click();

    // GetTokenAttribution is stubbed empty by default — the tab's own empty
    // state renders instead of an error.
    await expect(modal.getByText(/No recorded tool output/i)).toBeVisible({ timeout: 5_000 });

    // Override the stub to return one signature/tool, then reload via Refresh.
    await page.evaluate(() => {
      const w = window as any;
      w.go.main.App.GetTokenAttribution = () =>
        Promise.resolve({
          TotalEstTokens: 1000,
          BySignature: [
            {
              Sig: 'Read:src/*.go',
              Tool: 'Read',
              Count: 10,
              EstTokens: 1000,
              Share: 1,
              AvgResultChars: 4000,
              MaxResultChars: 4000,
            },
          ],
          ByTool: [{ Tool: 'Read', Count: 10, EstTokens: 1000, Share: 1 }],
        });
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByText('Read:src/*.go')).toBeVisible({ timeout: 5_000 });
    // formatTokens(1000) -> "1.0k".
    await expect(modal.getByText(/1\.0k est\. tokens total/i)).toBeVisible();

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
