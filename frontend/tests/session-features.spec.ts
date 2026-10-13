/**
 * Session features popup — SessionFeatures.svelte, opened from the ⚙ icon
 * in front of each session's name in the Sidebar.
 *
 * Lists every catalogued setting (frontend/src/lib/sessionFeatures.ts) as
 * ON / OFF with a plain-language description and lets the user edit it in
 * place. Every edit is saved at once through UpdateConfig; a start-time
 * setting changed while the session runs offers a restart.
 */

import { test, expect } from './fixtures';

const PROJECT = 'test';
const ID = (name: string) => `${PROJECT}/${name}`;

async function primerOf(ctrl: any, session: string): Promise<boolean> {
  const cfg = await ctrl.rpc('GetConfig', {});
  const p = cfg.Projects.find((x: any) => x.Name === PROJECT);
  return !!p.Sessions.find((x: any) => x.Name === session).ContextPrimer;
}

test.describe('Session features popup', () => {
  test('opens from the sidebar icon, lists features with states, closes on Escape', async ({ page }) => {
    await page.goto('/');
    await page.locator(`[data-testid="session-features-button-${ID('S1')}"]`).click();

    const dialog = page.locator('[data-testid="session-features"]');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    await expect(dialog).toContainText(ID('S1'));
    await expect(dialog.locator('[data-testid="features-summary"]')).toHaveText(/\d+ of \d+ on/);

    // An off-by-default opt-in shows OFF with its description and editor.
    const primer = dialog.locator('[data-testid="feature-contextPrimer"]');
    await expect(primer).toHaveAttribute('data-enabled', 'false');
    await expect(primer.locator('[data-testid="feature-state"]')).toHaveText('OFF');
    await expect(primer.locator('[data-testid="field-context_primer"]')).not.toBeChecked();
    await expect(primer).toContainText('applies on session start');

    // Every catalogued setting is wired: no "not wired" marker anywhere,
    // and manager-side ones say they apply at once.
    await expect(dialog).not.toContainText('NOT WIRED');
    await expect(dialog.locator('[data-testid="feature-permissionSound"]')).toContainText('applies at once');

    // The filter hides plain ON items.
    await dialog.getByLabel('Only off / with warnings').check();
    await expect(dialog.locator('[data-enabled="true"]:not(:has-text("⚠"))')).toHaveCount(0);

    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
  });

  test('a toggle is saved to the config at once', async ({ page, ctrl }) => {
    expect(await primerOf(ctrl, 'S2')).toBe(false);
    await page.goto('/');
    await page.locator(`[data-testid="session-features-button-${ID('S2')}"]`).click();
    const dialog = page.locator('[data-testid="session-features"]');
    const primer = dialog.locator('[data-testid="feature-contextPrimer"]');

    await primer.locator('[data-testid="field-context_primer"]').check();
    await expect(primer.locator('[data-testid="feature-saved"]')).toBeVisible({ timeout: 5_000 });
    await expect(primer.locator('[data-testid="feature-state"]')).toHaveText('ON');
    expect(await primerOf(ctrl, 'S2')).toBe(true);
    // Idle session: nothing to restart.
    await expect(dialog.locator('[data-testid="features-needs-restart"]')).toHaveCount(0);

    // Restore so other specs see the default.
    await primer.locator('[data-testid="field-context_primer"]').uncheck();
    await expect.poll(() => primerOf(ctrl, 'S2'), { timeout: 5_000 }).toBe(false);

    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
  });

  test('changing a start-time setting of a running session offers a restart', async ({ page, ctrl }) => {
    await ctrl.rpc('StopSession', { id: ID('S1'), soft: false }).catch(() => {});
    await page.goto('/');
    await ctrl.rpc('StartSession', { project: PROJECT, session: 'S1' });
    await ctrl.wait('session:status', { id: ID('S1'), status: 'working' }, 12_000);

    await page.locator(`[data-testid="session-features-button-${ID('S1')}"]`).click();
    const dialog = page.locator('[data-testid="session-features"]');
    const primer = dialog.locator('[data-testid="feature-contextPrimer"]');

    await primer.locator('[data-testid="field-context_primer"]').check();
    await expect(dialog.locator('[data-testid="features-needs-restart"]')).toBeVisible({ timeout: 5_000 });
    await expect(dialog.locator('[data-testid="features-restart"]')).toBeEnabled();

    await primer.locator('[data-testid="field-context_primer"]').uncheck();
    await expect.poll(() => primerOf(ctrl, 'S1'), { timeout: 5_000 }).toBe(false);
    await page.keyboard.press('Escape');
    await ctrl.rpc('StopSession', { id: ID('S1'), soft: false }).catch(() => {});
  });
});
