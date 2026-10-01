/**
 * Skills tab spec — LEARN-TASKS.md LN-10
 *
 * GetSkills/ApproveSkill/ArchiveSkill read/write the app's own SQLite store,
 * which playwright-server never opens (cmd/playwright-server/main.go passes
 * SessionManager a nil store — same reason History.svelte/CostDashboard.svelte
 * and the other Experience tabs have no real-data Playwright coverage).
 * helpers/bridge.ts stubs all three empty by default; this spec overrides them
 * per test to drive the review→edit→accept/archive flow end to end against the
 * DOM, and checks ApproveSkill/ArchiveSkill are actually called with the right
 * arguments — the closest thing to "проверка через control-plane" available
 * here, since neither binding is on control.AppAPI (Wails-only, same as
 * GetTopActions/AddPermissionRule).
 */

import { test, expect } from './fixtures';

async function openSkillsTab(page: import('@playwright/test').Page) {
  await page.goto('/');
  const experienceBtn = page.getByRole('button', { name: 'Experience' });
  await expect(experienceBtn).toBeVisible({ timeout: 5_000 });
  await experienceBtn.click();
  const modal = page.getByRole('dialog');
  await expect(modal).toBeVisible({ timeout: 3_000 });
  await modal.getByRole('button', { name: 'Skills' }).click();
  return modal;
}

test.describe('Skills tab', () => {
  test('shows the empty state when there are no distilled skills', async ({ page }) => {
    const modal = await openSkillsTab(page);
    await expect(modal.getByText(/No distilled skills/i)).toBeVisible({ timeout: 5_000 });
  });

  test('draft → edit → Accept writes the edited markdown and marks it approved', async ({
    page,
  }) => {
    const modal = await openSkillsTab(page);

    await page.evaluate(() => {
      const w = window as any;
      w.__approveCalls = [] as unknown[];
      let approved = false;
      const App = w.go.main.App;
      App.GetSkills = () =>
        Promise.resolve([
          {
            ID: 1,
            Project: 'test',
            Name: 'git-session-preamble',
            Status: approved ? 'approved' : 'draft',
            DraftJSON: JSON.stringify({
              name: 'git-session-preamble',
              description: 'Run git status/branch before touching anything.',
            }),
            MD: '---\nname: git-session-preamble\n---\noriginal body',
            SourceJSON: '["Bash:git status"]',
            CreatedAt: new Date().toISOString(),
            ApprovedAt: null,
            ArchivedAt: null,
          },
        ]);
      App.ApproveSkill = (id: number, md: string, overwrite: boolean) => {
        w.__approveCalls.push([id, md, overwrite]);
        approved = true;
        return Promise.resolve('/proj/.claude/skills/git-session-preamble/SKILL.md');
      };
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByText('git-session-preamble')).toBeVisible({ timeout: 5_000 });
    await expect(modal.getByText('Run git status/branch before touching anything.')).toBeVisible();

    await modal.getByText('git-session-preamble').click();
    const textarea = modal.locator('textarea');
    await expect(textarea).toBeVisible({ timeout: 3_000 });
    await expect(textarea).toHaveValue(/original body/);
    await textarea.fill('---\nname: git-session-preamble\n---\nedited body');

    await modal.getByRole('button', { name: 'Accept' }).click();

    const calls = await page.evaluate(() => (window as any).__approveCalls);
    expect(calls).toEqual([[1, '---\nname: git-session-preamble\n---\nedited body', false]]);

    // Approving closes the review panel and reloads — the row now shows Approved.
    await expect(modal.getByText('Active', { exact: true })).toBeVisible({ timeout: 5_000 });
  });

  test('re-approving an existing file shows the overwrite banner, and overwriting succeeds', async ({
    page,
  }) => {
    const modal = await openSkillsTab(page);

    await page.evaluate(() => {
      const w = window as any;
      w.__approveCalls = [] as unknown[];
      const App = w.go.main.App;
      App.GetSkills = () =>
        Promise.resolve([
          {
            ID: 2,
            Project: 'test',
            Name: 'existing-skill',
            Status: 'draft',
            DraftJSON: JSON.stringify({ name: 'existing-skill', description: 'Already on disk.' }),
            MD: 'body',
            SourceJSON: '[]',
            CreatedAt: new Date().toISOString(),
            ApprovedAt: null,
            ArchivedAt: null,
          },
        ]);
      App.ApproveSkill = (id: number, md: string, overwrite: boolean) => {
        w.__approveCalls.push([id, md, overwrite]);
        if (!overwrite) {
          return Promise.reject(new Error('experience: skill file already exists'));
        }
        return Promise.resolve('/proj/.claude/skills/existing-skill/SKILL.md');
      };
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await modal.getByText('existing-skill').click();
    await modal.getByRole('button', { name: 'Accept' }).click();

    await expect(modal.getByText(/already exists/i)).toBeVisible({ timeout: 5_000 });
    await modal.getByRole('button', { name: 'Yes, overwrite' }).click();

    const calls = await page.evaluate(() => (window as any).__approveCalls);
    expect(calls).toEqual([
      [2, 'body', false],
      [2, 'body', true],
    ]);
  });

  test('Archive removes the skill from the active list', async ({ page }) => {
    const modal = await openSkillsTab(page);

    await page.evaluate(() => {
      const w = window as any;
      w.__archiveCalls = [] as unknown[];
      let archived = false;
      const App = w.go.main.App;
      App.GetSkills = () =>
        Promise.resolve(
          archived
            ? []
            : [
                {
                  ID: 3,
                  Project: 'test',
                  Name: 'stale-skill',
                  Status: 'draft',
                  DraftJSON: JSON.stringify({ name: 'stale-skill', description: 'Not needed.' }),
                  MD: 'body',
                  SourceJSON: '[]',
                  CreatedAt: new Date().toISOString(),
                  ApprovedAt: null,
                  ArchivedAt: null,
                },
              ],
        );
      App.ArchiveSkill = (id: number) => {
        w.__archiveCalls.push([id]);
        archived = true;
        return Promise.resolve(undefined);
      };
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByText('stale-skill')).toBeVisible({ timeout: 5_000 });
    await modal.getByText('stale-skill').click();
    await modal.getByRole('button', { name: 'Archive' }).click();

    const calls = await page.evaluate(() => (window as any).__archiveCalls);
    expect(calls).toEqual([[3]]);

    await expect(modal.getByText(/No distilled skills/i)).toBeVisible({ timeout: 5_000 });
  });
});

// LEARN-TASKS.md LN-08 — the "Candidates" list mined from action history and
// its Distill button, the only source of an experience.SkillCandidate to
// pass to DistillSkill (LN-09).
test.describe('Skills tab — candidates (LN-08)', () => {
  test('lists a mined candidate and Distill calls DistillSkill with it', async ({ page }) => {
    const modal = await openSkillsTab(page);

    await page.evaluate(() => {
      const w = window as any;
      w.__distillCalls = [] as unknown[];
      const App = w.go.main.App;
      App.GetSkillCandidates = () =>
        Promise.resolve([
          {
            Sig: ['Bash:git status', 'Bash:git add <ARG>'],
            DistinctRuns: 4,
            RunShare: 0.5,
            Score: 12.3,
            Samples: [],
            RelatedFailures: [],
            ContextLossSuspect: false,
            Imported: false,
            FirstSeen: new Date().toISOString(),
            LastSeen: new Date().toISOString(),
          },
        ]);
      App.DistillSkill = (project: string, candidate: unknown, gates: unknown, model: string, minScore: number) => {
        w.__distillCalls.push([project, candidate, gates, model, minScore]);
        App.GetSkills = () =>
          Promise.resolve([
            {
              ID: 9,
              Project: project,
              Name: 'git-status-then-add',
              Status: 'draft',
              DraftJSON: JSON.stringify({
                name: 'git-status-then-add',
                description: 'Stage after checking status.',
              }),
              MD: 'body',
              // Go's json.Marshal escapes < and > — mirror it (regression: the
              // "already distilled" match used to compare raw strings).
              SourceJSON: JSON.stringify((candidate as { Sig: string[] }).Sig).replace(/</g, '\\u003c').replace(/>/g, '\\u003e'),
              CreatedAt: new Date().toISOString(),
              ApprovedAt: null,
              ArchivedAt: null,
            },
          ]);
        return Promise.resolve({
          ID: 9,
          Project: project,
          Name: 'git-status-then-add',
          Status: 'draft',
          DraftJSON: '{}',
          MD: 'body',
          SourceJSON: '[]',
          CreatedAt: new Date().toISOString(),
          ApprovedAt: null,
          ArchivedAt: null,
        });
      };
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByText('Bash:git status → Bash:git add <ARG>')).toBeVisible({ timeout: 5_000 });
    await modal.getByRole('button', { name: 'Distill' }).click();

    await expect(modal.getByText('git-status-then-add', { exact: true })).toBeVisible({ timeout: 5_000 });
    const calls = await page.evaluate(() => (window as any).__distillCalls);
    expect(calls.length).toBe(1);
    expect(calls[0][0]).toBe('test');
    expect(calls[0][1].Sig).toEqual(['Bash:git status', 'Bash:git add <ARG>']);
    await expect(modal.getByText(/Already distilled → git-status-then-add/)).toBeVisible();
    // The new draft is opened for review, and the candidate row now offers
    // "Open draft" instead of a second Distill.
    await expect(modal.locator('textarea')).toBeVisible();
    await expect(modal.getByRole('button', { name: 'Open draft' })).toBeVisible();
    // Re-distilling is two-click: the first click only arms the button.
    await modal.getByRole('button', { name: 'Distill again' }).click();
    expect((await page.evaluate(() => (window as any).__distillCalls)).length).toBe(1);
    await modal.getByRole('button', { name: 'Click again to confirm' }).click();
    await expect.poll(() => page.evaluate(() => (window as any).__distillCalls.length)).toBe(2);
  });
});

// LEARN-TASKS.md LN-11 — the before/after-approval effect, shown in the
// library's "Effect" column of each live skill.
test.describe('Skills tab — effect column (LN-11)', () => {
  test('shows before → after tokens, stale hints and "not enough data"', async ({ page }) => {
    const modal = await openSkillsTab(page);

    await page.evaluate(() => {
      const w = window as any;
      const App = w.go.main.App;
      const row = (id: number, name: string) => ({
        ID: id, Project: 'test', Name: name, Status: 'approved',
        DraftJSON: JSON.stringify({ name, description: 'Desc ' + name }),
        MD: 'body', SourceJSON: '[]', CreatedAt: new Date().toISOString(),
        ApprovedAt: new Date().toISOString(), ArchivedAt: null,
        Origin: 'manual', Reason: '', CostUSD: 0, UpdatedAt: null,
      });
      const stats = (runs: number, tok: number, rate: number) =>
        ({ runs, median_input_tokens: tok, median_num_turns: 5, completed_rate: rate });
      App.GetSkills = () => Promise.resolve([row(1, 'run-go-tests'), row(2, 'stale-no-improvement'), row(3, 'unused-skill')]);
      App.GetSkillQuality = () =>
        Promise.resolve([
          { skill_id: 1, skill_name: 'run-go-tests', approved_at: '', before: stats(3, 11000, 0.67), after: stats(3, 4000, 1),
            insufficient_data: false, stale: false },
          { skill_id: 2, skill_name: 'stale-no-improvement', approved_at: '', before: stats(3, 5000, 1), after: stats(5, 6000, 1),
            insufficient_data: false, stale: true, stale_reason: 'no_improvement' },
          { skill_id: 3, skill_name: 'unused-skill', approved_at: '', before: stats(0, 0, 0), after: stats(0, 0, 0),
            insufficient_data: true, stale: true, stale_reason: 'unused' },
        ]);
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByText('run-go-tests', { exact: true })).toBeVisible({ timeout: 5_000 });
    await expect(modal.getByText('11.0k → 4.0k')).toBeVisible();
    await expect(modal.getByText(/Suggest archiving \(no token improvement/i)).toBeVisible();
    await expect(modal.getByText(/Suggest archiving \(unused/i)).toBeVisible();
  });
});

// LEARN-TASKS.md LN-24..27 — the autopilot header and the skill library.
test.describe('Skills tab — autopilot (LN-27)', () => {
  test('toggle persists via SetSkillAutopilot; library shows status, usage and reason', async ({ page }) => {
    const modal = await openSkillsTab(page);

    await page.evaluate(() => {
      const w = window as any;
      w.__setCalls = [] as unknown[];
      let enabled = false;
      const App = w.go.main.App;
      App.GetSkillAutopilot = () =>
        Promise.resolve({ enabled, tracking: true, daily_budget_usd: 0, spent_today_usd: 0.3, spent_total_usd: 0.5, trial_runs: 10 });
      App.SetSkillAutopilot = (p: string, on: boolean, b: number) => {
        w.__setCalls.push([p, on, b]);
        enabled = on;
        return Promise.resolve(undefined);
      };
      const row = (id: number, name: string, status: string, reason: object) => ({
        ID: id, Project: 'test', Name: name, Status: status,
        DraftJSON: JSON.stringify({ name, description: 'Desc ' + name }),
        MD: 'body', SourceJSON: '[]', CreatedAt: new Date().toISOString(),
        ApprovedAt: new Date().toISOString(), ArchivedAt: null,
        Origin: 'auto', Reason: JSON.stringify(reason), CostUSD: 0.25, UpdatedAt: null,
      });
      App.GetSkills = () =>
        Promise.resolve([
          row(1, 'kept-skill', 'approved', { code: 'kept', loads: 4, runs: 10 }),
          row(2, 'dead-skill', 'archived', { code: 'unused_trial', runs: 10 }),
        ]);
      App.GetSkillUsage = () =>
        Promise.resolve({
          1: { skill_id: 1, name: 'kept-skill', loads: 7, runs_since_applied: 12, runs_with_load: 4, runs_since_last_load: 1 },
        });
    });
    await modal.getByRole('button', { name: 'Refresh' }).click();

    await expect(modal.getByTestId('skill-autopilot')).toContainText('off');
    await expect(modal.getByText('kept-skill', { exact: true })).toBeVisible({ timeout: 5_000 });
    await expect(modal.getByText('Kept: loaded in 4 of 10 trial runs')).toBeVisible();
    await expect(modal.getByText('loaded 7×')).toBeVisible();
    await expect(modal.getByText('Switched off: not loaded once in 10 trial runs')).toBeVisible();

    await modal.getByRole('switch').click();
    await expect(modal.getByTestId('skill-autopilot')).toContainText('on');
    const calls = await page.evaluate(() => (window as any).__setCalls);
    expect(calls).toEqual([['test', true, 0]]);

    // Filter: only live skills.
    await modal.getByRole('button', { name: /^Active/ }).click();
    await expect(modal.getByText('dead-skill', { exact: true })).toHaveCount(0);
  });
});
