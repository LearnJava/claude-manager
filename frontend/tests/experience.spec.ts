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
              LifetimeTokens: 23500,
              LifetimeShare: 1,
            },
          ],
          ByTool: [
            { Tool: 'Read', Count: 10, EstTokens: 1000, Share: 1, LifetimeTokens: 23500, LifetimeShare: 1 },
          ],
          TotalLifetimeTokens: 23500,
          ByLength: [
            { MinTurns: 1, MaxTurns: 20, Runs: 6, CostUSD: 2.5, CostShare: 0.2, AvgCostUSD: 0.42 },
            { MinTurns: 251, MaxTurns: 0, Runs: 1, CostUSD: 10, CostShare: 0.8, AvgCostUSD: 10 },
          ],
        });
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByText('Read:src/*.go')).toBeVisible({ timeout: 5_000 });
    // formatTokens(1000) -> "1.0k".
    await expect(modal.getByText(/1\.0k est\. tokens total/i)).toBeVisible();

    // LN-29: the re-read price next to the volume, and the session-length cut.
    await expect(modal.getByRole('columnheader', { name: 'With re-reads' }).first()).toBeVisible();
    await expect(modal.getByText('23.5k').first()).toBeVisible();
    const byLength = modal.getByTestId('cost-by-length');
    await expect(byLength).toBeVisible();
    await expect(byLength.getByText('1–20')).toBeVisible();
    await expect(byLength.getByText('251+')).toBeVisible();
    await expect(byLength.getByText('80%')).toBeVisible();

    await modal.getByRole('button', { name: '✕' }).click();
    await expect(modal).not.toBeVisible({ timeout: 3_000 });
  });

  test('Waste tab: clean window, then categories with examples (WS-01)', async ({ page }) => {
    await page.goto('/');

    await page.getByRole('button', { name: 'Experience' }).click();
    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });

    await modal.getByTestId('tab-waste').click();
    // GetWasteReport is stubbed with no calls — the empty state, not an error.
    await expect(modal.getByText(/No recorded tool calls/i)).toBeVisible({ timeout: 5_000 });

    await page.evaluate(() => {
      const w = window as any;
      w.go.main.App.GetWasteReport = () =>
        Promise.resolve({
          TotalCalls: 50,
          TotalLifetimeTokens: 10000,
          WastedCalls: 4,
          WastedLifetimeTokens: 2500,
          WastedShare: 0.25,
          LimitWindowTokens: 6250,
          WindowKnown: true,
          WindowShare: 0.4,
          Categories: [
            {
              Category: 'repeat_read',
              Count: 3,
              EstTokens: 1000,
              LifetimeTokens: 2000,
              Share: 0.2,
              Examples: [
                { Session: 'P1', RunID: 7, Step: 12, Tool: 'Read', Arg: 'a.go', Tokens: 500, LifetimeTokens: 1000 },
              ],
            },
            { Category: 'rerun_cmd', Count: 1, EstTokens: 100, LifetimeTokens: 500, Share: 0.05, Examples: [] },
          ],
        });
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByTestId('waste-headline')).toContainText('4 of 50 calls wasted');
    await expect(modal.getByTestId('waste-headline')).toContainText('25%');
    await expect(modal.getByTestId('waste-cat-repeat_read')).toContainText('Repeated reads');
    await expect(modal.getByTestId('waste-cat-rerun_cmd')).toBeVisible();
    await expect(modal.getByTestId('waste-report')).toContainText('Read a.go — P1 · run 7 · step 12');
    // WS-02: on a subscription the loss is shown as a slice of the limit window.
    await expect(modal.getByTestId('waste-window-share')).toContainText('40% of the limit window');

    // Window size not configured: say so instead of printing 0%.
    await page.evaluate(() => {
      (window as any).go.main.App.GetWasteReport = () =>
        Promise.resolve({
          TotalCalls: 50,
          TotalLifetimeTokens: 10000,
          WastedCalls: 1,
          WastedLifetimeTokens: 500,
          WastedShare: 0.05,
          LimitWindowTokens: 0,
          WindowKnown: false,
          WindowShare: 0,
          Categories: [{ Category: 'rerun_cmd', Count: 1, EstTokens: 100, LifetimeTokens: 500, Share: 0.05, Examples: [] }],
        });
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();
    await expect(modal.getByTestId('waste-window-share')).toContainText('limit window unknown');
    await expect(modal.getByTestId('waste-window-share')).not.toContainText('0%');

    // No category occurred: a clean message, not a table of zeros.
    await page.evaluate(() => {
      (window as any).go.main.App.GetWasteReport = () =>
        Promise.resolve({
          TotalCalls: 9,
          TotalLifetimeTokens: 900,
          WastedCalls: 0,
          WastedLifetimeTokens: 0,
          WastedShare: 0,
          Categories: [],
        });
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();
    await expect(modal.getByTestId('waste-clean')).toBeVisible({ timeout: 5_000 });
  });

  test('Import logs button: menu, progress event, and result summary', async ({ page }) => {
    await page.goto('/');

    const experienceBtn = page.getByRole('button', { name: 'Experience' });
    await expect(experienceBtn).toBeVisible({ timeout: 5_000 });
    await experienceBtn.click();

    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });

    // Override ImportProjectLogs to emit one progress event, then resolve
    // with a populated ImportStats — mirrors how session.ImportProjectLogs
    // streams experience:import while IngestDir's walk is in flight.
    await page.evaluate(() => {
      const w = window as any;
      w.go.main.App.ImportProjectLogs = (project: string) => {
        w.__dispatchWailsEvent('experience:import', { project, processed: 1, total: 2 });
        return new Promise((resolve) =>
          setTimeout(
            () => resolve({ Files: 2, Runs: 2, Actions: 10, Skipped: 1, Errors: 0 }),
            50,
          ),
        );
      };
    });

    await modal.getByRole('button', { name: 'Import logs' }).click();
    await modal.getByText("This project's logs").click();

    await expect(modal.getByText(/Imported 1 \/ 2 files/i)).toBeVisible({ timeout: 3_000 });
    await expect(
      modal.getByText(/Files: 2, runs: 2, actions: 10, skipped: 1, errors: 0/i),
    ).toBeVisible({ timeout: 3_000 });

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
        .getByText('Remember what agents did in finished runs')
        .locator('input[type="checkbox"]');
    const wasChecked = await checkbox.isChecked();
    await checkbox.click();
    expect(await checkbox.isChecked()).toBe(!wasChecked);

    const saveBtn = modal.getByRole('button', { name: 'Save' });
    await saveBtn.click();
    await expect(modal.getByText('Saved.', { exact: true })).toBeVisible({ timeout: 5_000 });

    const cfg = await ctrl.rpc<{ Optimization?: { ExperienceTracking?: boolean } }>(
      'GetConfig',
      {},
    );
    expect(cfg.Optimization?.ExperienceTracking).toBe(!wasChecked);

    // Restore so other tests are not affected by the flip.
    await checkbox.click();
    await saveBtn.click();
    await expect(modal.getByText('Saved.', { exact: true })).toBeVisible({ timeout: 5_000 });

    await modal.getByRole('button', { name: 'Close' }).click();
    await expect(modal).not.toBeVisible({ timeout: 3_000 });
  });
  test('Failures tab: drafts render, Accept sends the edited text, Suggest blocks while running', async ({
    page,
  }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Experience' }).click();
    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });

    await page.evaluate(() => {
      const w = window as any;
      const App = w.go.main.App;
      w.__accept = [] as unknown[];
      w.__releaseDraft = () => {};
      let drafts = [
        {
          ID: 7,
          Project: 'test',
          ErrorKey: 'Bash: No such file',
          RuleText: 'Check the path first',
          Status: 'draft',
          DistinctRuns: 4,
          CostUSD: 0,
          CreatedAt: new Date().toISOString(),
          DecidedAt: null,
          Examples: [{ failed_arg: 'cat a.txt', err_text: 'No such file', fixed_arg: 'cat ./a.txt' }],
          Effect: { RunsBefore: 0, HitsBefore: 0, RunsAfter: 0, HitsAfter: 0 },
        },
      ];
      App.GetFailureRules = () => Promise.resolve({ Rules: drafts, Pending: 2 });
      App.AcceptFailureRule = (...args: unknown[]) => {
        w.__accept.push(args);
        drafts = [];
        return Promise.resolve(undefined);
      };
      App.DraftFailureRules = () => new Promise((res) => (w.__releaseDraft = () => res([])));
    });

    await modal.getByRole('button', { name: 'Failures' }).click();
    await expect(modal.getByText('Recurring errors without a rule: 2')).toBeVisible({ timeout: 5_000 });
    await expect(modal.getByText('Bash: No such file')).toBeVisible();
    await expect(modal.getByText('in 4 runs')).toBeVisible();

    // Suggest rules disables the button for the duration of the call.
    await modal.getByRole('button', { name: 'Suggest rules' }).click();
    await expect(modal.getByRole('button', { name: 'Drafting rules…' })).toBeDisabled();
    await page.evaluate(() => (window as any).__releaseDraft());
    await expect(modal.getByRole('button', { name: 'Suggest rules' })).toBeEnabled({ timeout: 3_000 });

    await modal.locator('textarea').fill('Always use ./ paths');
    await modal.getByRole('button', { name: 'Accept into CLAUDE.md' }).click();
    await expect.poll(() => page.evaluate(() => (window as any).__accept)).toEqual([[7, 'Always use ./ paths']]);
  });

  test('Failures tab: autopilot rules can be switched off and brought back', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Experience' }).click();
    const modal = page.getByRole('dialog');
    await expect(modal).toBeVisible({ timeout: 3_000 });

    await page.evaluate(() => {
      const w = window as any;
      const App = w.go.main.App;
      w.__revert = [] as unknown[];
      w.__restore = [] as unknown[];
      const base = {
        Project: 'test', ErrorKey: 'k', DistinctRuns: 3, CostUSD: 0, CreatedAt: new Date().toISOString(),
        DecidedAt: new Date().toISOString(), Origin: 'auto', Examples: null, TrialRuns: 10,
        Effect: { RunsBefore: 20, HitsBefore: 8, RunsAfter: 4, HitsAfter: 1 }, Why: null,
      };
      const rules = [
        { ...base, ID: 1, RuleText: 'Rule on trial', Status: 'trial' },
        {
          ...base, ID: 2, RuleText: 'Rule thrown out', Status: 'reverted',
          Why: { code: 'no_effect', hits_before: 8, runs_before: 20, hits_after: 5, runs_after: 10, completed_before: 0, completed_after: 0 },
        },
      ];
      App.GetFailureRules = () => Promise.resolve({ Rules: rules, Pending: 0 });
      App.RevertFailureRule = (...a: unknown[]) => { w.__revert.push(a); return Promise.resolve(undefined); };
      App.RestoreFailureRule = (...a: unknown[]) => { w.__restore.push(a); return Promise.resolve(undefined); };
    });

    await modal.getByRole('button', { name: 'Failures' }).click();
    await expect(modal.getByText('Applied by the autopilot (2)')).toBeVisible({ timeout: 5_000 });
    await expect(modal.getByText('trial: 4 of 10 runs')).toBeVisible();
    await expect(modal.getByText('the error did not get rarer', { exact: false })).toBeVisible();

    await modal.getByRole('button', { name: 'Switch off' }).click();
    await expect.poll(() => page.evaluate(() => (window as any).__revert)).toEqual([[1]]);
    await modal.getByRole('button', { name: 'Bring back' }).click();
    await expect.poll(() => page.evaluate(() => (window as any).__restore)).toEqual([[2]]);
  });
});
