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
    await expect(modal.getByText('Approved')).toBeVisible({ timeout: 5_000 });
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
