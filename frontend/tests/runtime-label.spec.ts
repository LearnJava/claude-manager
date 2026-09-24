/**
 * Runtime label spec — VIEW-TASKS.md UI-06 (SessionCard.svelte — SC-07).
 *
 * SessionCard's second row must show a large "Use Hermes" / "Use Claude Code"
 * label next to the model, driven purely by `session.runtime`:
 *   - S1 (runtime unset, the default) -> "Use Claude Code"
 *   - S4 (runtime = "hermes", fakehermes) -> "Use Hermes"
 */

import { test, expect } from './fixtures';

const PROJECT = 'test';
const SESSION_ID = (name: string) => `${PROJECT}/${name}`;

test.describe('SessionCard runtime label', () => {
  test.beforeEach(async ({ ctrl }) => {
    await ctrl.rpc('StopSession', { id: SESSION_ID('S1'), soft: false }).catch(() => {});
    await ctrl.rpc('StopSession', { id: SESSION_ID('S4'), soft: false }).catch(() => {});
    await new Promise((r) => setTimeout(r, 200));
  });

  test('claude-runtime session shows "Use Claude Code"', async ({ page, ctrl }) => {
    await page.goto('/');
    await ctrl.rpc('StartSession', { project: PROJECT, session: 'S1' });
    await ctrl.wait('session:status', { id: SESSION_ID('S1'), status: 'working' }, 12_000);

    const sessionRow = page.locator('[role="button"]', { hasText: 'S1' }).first();
    await expect(sessionRow).toBeVisible({ timeout: 5_000 });
    await sessionRow.click();

    const label = page.locator('[data-testid="runtime-label"]');
    await expect(label).toBeVisible({ timeout: 5_000 });
    await expect(label).toHaveText('Use Claude Code');
  });

  test('hermes-runtime session shows "Use Hermes"', async ({ page, ctrl }) => {
    await page.goto('/');
    await ctrl.rpc('StartSession', { project: PROJECT, session: 'S4' });
    await ctrl.wait('session:status', { id: SESSION_ID('S4'), status: 'working' }, 12_000);

    const sessionRow = page.locator('[role="button"]', { hasText: 'S4' }).first();
    await expect(sessionRow).toBeVisible({ timeout: 5_000 });
    await sessionRow.click();

    const label = page.locator('[data-testid="runtime-label"]');
    await expect(label).toBeVisible({ timeout: 5_000 });
    await expect(label).toHaveText('Use Hermes');
  });
});
