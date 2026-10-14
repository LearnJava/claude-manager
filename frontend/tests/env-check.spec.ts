/**
 * Settings → Global → Environment (EnvironmentCheck.svelte).
 *
 * CheckEnvironment is stubbed in helpers/bridge.ts with a fixed report: one
 * found tool, one missing with a winget command, one with a wrong version,
 * one optional without an install command.
 */

import { test, expect } from './fixtures';

test('environment section lists tools and install commands', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings' }).click();
  const modal = page.getByRole('dialog');
  const section = modal.getByTestId('env-check');
  await expect(section).toBeVisible({ timeout: 3_000 });

  // gh missing + wails mismatch; the optional bc does not count.
  await expect(section.getByTestId('env-check-summary')).toHaveText('Need attention: 2');

  await expect(section.getByTestId('env-tool-claude')).toContainText('2.1.296 (Claude Code)');
  await expect(section.getByTestId('env-install-claude')).toHaveCount(0);

  await expect(section.getByTestId('env-install-gh')).toHaveText('winget install --id GitHub.cli -e');

  const wails = section.getByTestId('env-tool-wails');
  await expect(wails).toContainText('needs v2.16.0');
  await expect(section.getByTestId('env-install-wails')).toHaveText(
    'go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0',
  );

  const bc = section.getByTestId('env-tool-bc');
  await expect(bc).toContainText('Git for Windows does not ship it');
  await expect(section.getByTestId('env-install-bc')).toHaveCount(0);
});
