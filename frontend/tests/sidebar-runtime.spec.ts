/**
 * Sidebar CLI/model pickers — every session row carries a CLI picker
 * (Claude / Hermes) next to the model picker, both mirroring the session's
 * config (what the next start really runs):
 *   - S1 (runtime unset)   -> "claude", model from config
 *   - S4 (runtime=hermes)  -> "hermes"
 *   - switching S1's CLI persists `runtime` in config and survives a reload
 *   - the CLI picker is locked while the session runs (S3)
 */

import { test, expect } from './fixtures';

const PROJECT = 'test';
const SESSION_ID = (name: string) => `${PROJECT}/${name}`;

type Cfg = { Projects?: Array<{ Name: string; Sessions?: Array<{ Name: string; Runtime?: string }> }> };

async function configuredRuntime(ctrl: { rpc: (m: string, p: object) => Promise<unknown> }, name: string) {
  const cfg = (await ctrl.rpc('GetConfig', {})) as Cfg;
  return cfg.Projects?.find((p) => p.Name === PROJECT)?.Sessions?.find((s) => s.Name === name)?.Runtime ?? '';
}

async function setConfiguredRuntime(ctrl: { rpc: (m: string, p: object) => Promise<unknown> }, name: string, runtime: string) {
  const cfg = (await ctrl.rpc('GetConfig', {})) as Cfg;
  const sc = cfg.Projects?.find((p) => p.Name === PROJECT)?.Sessions?.find((s) => s.Name === name);
  if (!sc) throw new Error(`no session ${name}`);
  sc.Runtime = runtime;
  await ctrl.rpc('UpdateConfig', { config: cfg });
}

test.describe('Sidebar CLI picker', () => {
  test.beforeEach(async ({ ctrl }) => {
    await ctrl.rpc('StopSession', { id: SESSION_ID('S1'), soft: false }).catch(() => {});
    await ctrl.rpc('StopSession', { id: SESSION_ID('S4'), soft: false }).catch(() => {});
    await new Promise((r) => setTimeout(r, 200));
  });

  test.afterEach(async ({ ctrl }) => {
    await setConfiguredRuntime(ctrl, 'S1', '');
  });

  test('pickers reflect each session config', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByTestId(`runtime-select-${SESSION_ID('S1')}`)).toHaveValue('claude', { timeout: 5_000 });
    await expect(page.getByTestId(`runtime-select-${SESSION_ID('S4')}`)).toHaveValue('hermes');
    // claude-sonnet-4-6 in config folds onto the "sonnet" alias for Claude.
    await expect(page.getByTestId(`model-select-${SESSION_ID('S1')}`)).toHaveValue('sonnet');
  });

  test('switching the CLI persists it in config', async ({ page, ctrl }) => {
    await page.goto('/');
    const picker = page.getByTestId(`runtime-select-${SESSION_ID('S1')}`);
    await expect(picker).toHaveValue('claude', { timeout: 5_000 });

    await picker.selectOption('hermes');
    await expect.poll(() => configuredRuntime(ctrl, 'S1')).toBe('hermes');
    await expect(picker).toHaveValue('hermes');

    await page.reload();
    await expect(page.getByTestId(`runtime-select-${SESSION_ID('S1')}`)).toHaveValue('hermes', { timeout: 5_000 });

    await page.getByTestId(`runtime-select-${SESSION_ID('S1')}`).selectOption('claude');
    await expect.poll(() => configuredRuntime(ctrl, 'S1')).toBe('');
  });

  // S3 is the multi-turn scenario: it waits on stdin, so it stays running
  // (S1's one-shot task is idle again within seconds).
  test('CLI picker is locked while the session runs', async ({ page, ctrl }) => {
    await page.goto('/');
    await ctrl.rpc('StartSession', { project: PROJECT, session: 'S3' });
    try {
      await ctrl.wait('session:status', { id: SESSION_ID('S3'), status: 'working' }, 12_000);
      await expect(page.getByTestId(`runtime-select-${SESSION_ID('S3')}`)).toBeDisabled({ timeout: 5_000 });
    } finally {
      await ctrl.rpc('StopSession', { id: SESSION_ID('S3'), soft: false }).catch(() => {});
    }
  });
});
