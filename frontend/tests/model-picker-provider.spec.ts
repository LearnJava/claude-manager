/**
 * Model picker provider segment (GLM-PLAN.md §7, §9): the picker offers the
 * backend-configured providers, the GLM segment lists exactly the six
 * model×effort combinations with their exact labels, and confirming a
 * combination starts the session through the provider path — in this harness
 * the bridge maps that to the Provider config field plus the model override,
 * which is what the real backend derives from the same start anyway.
 */

import { test, expect } from './fixtures';

const PROJECT = 'test';
const ID = (name: string) => `${PROJECT}/${name}`;
const SESSION = 'S1';

async function sessionCfg(ctrl: any): Promise<any> {
    const cfg = await ctrl.rpc('GetConfig', {});
    const p = cfg.Projects.find((x: any) => x.Name === PROJECT);
    return p.Sessions.find((x: any) => x.Name === SESSION);
}

async function setAutoModelRouting(ctrl: any, on: boolean): Promise<void> {
    const cfg = await ctrl.rpc('GetConfig', {});
    cfg.Optimization = cfg.Optimization ?? {};
    cfg.Optimization.AutoModelRouting = on;
    await ctrl.rpc('UpdateConfig', { config: cfg });
}

test.describe('Model picker: provider segment', () => {
    test('GLM segment lists exactly six combos; start remembers provider+model', async ({ page, ctrl }) => {
        const before = await sessionCfg(ctrl);
        await setAutoModelRouting(ctrl, true);
        try {
            await page.goto('/');
            const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
            await sessionRow.hover();
            await sessionRow.getByTitle('Start', { exact: true }).click();
            await expect(page.getByText('Start session')).toBeVisible({ timeout: 5_000 });

            // The segment appears with the backend-configured provider.
            await page.locator('[data-testid="provider-tab-glm"]').click();
            const select = page.locator('[data-testid="provider-combo-select"]');
            await expect(select).toBeVisible();

            // Exactly six combinations, exact labels.
            const options = select.locator('option');
            await expect(options).toHaveCount(6);
            for (let i = 0; i < 6; i++) {
                await expect(options.nth(i)).toHaveText(
                    ['GLM-5.3 low', 'GLM-5.3 high', 'GLM-5.3 max',
                     'GLM-5.3-Flash low', 'GLM-5.3-Flash high', 'GLM-5.3-Flash max'][i],
                );
            }

            // Manual-only: the analysis spinner never shows for a provider.
            await expect(page.locator('[data-testid="model-picker-analyzing"]')).toHaveCount(0);

            // Confirm "GLM-5.3 low" → provider start: model + provider are
            // remembered as the session's stored default.
            await select.selectOption('glm-5.3::low');
            await page.getByRole('button', { name: /Start with GLM-5\.3 low/ }).click();
            await expect
                .poll(
                    async () => {
                        const sc = await sessionCfg(ctrl);
                        return { model: sc.Model, provider: sc.Provider, effort: sc.Effort };
                    },
                    { timeout: 10_000 },
                )
                .toEqual({ model: 'glm-5.3', provider: 'glm', effort: 'low' });

            // The sidebar's live-switch dropdown shows the provider model.
            await expect
                .poll(
                    () => page.locator(`[data-testid="model-select-${ID(SESSION)}"]`).inputValue(),
                    { timeout: 10_000 },
                )
                .toBe('glm-5.3');
        } finally {
            await ctrl.rpc('StopSession', { id: ID(SESSION), soft: false }).catch(() => {});
            const cfg = await ctrl.rpc('GetConfig', {});
            const p = cfg.Projects.find((x: any) => x.Name === PROJECT);
            const sc = p.Sessions.find((x: any) => x.Name === SESSION);
            sc.Model = before.Model ?? '';
            sc.Effort = before.Effort ?? '';
            sc.Provider = before.Provider ?? '';
            await ctrl.rpc('UpdateConfig', { config: cfg });
            await setAutoModelRouting(ctrl, false);
        }
    });

    test('Claude segment stays the default and starts without a provider', async ({ page, ctrl }) => {
        const before = await sessionCfg(ctrl);
        await setAutoModelRouting(ctrl, true);
        try {
            await page.goto('/');
            const sessionRow = page.locator('[role="button"]', { hasText: SESSION }).first();
            await sessionRow.hover();
            await sessionRow.getByTitle('Start', { exact: true }).click();
            await expect(page.getByText('Start session')).toBeVisible({ timeout: 5_000 });

            // Claude is the active segment; the analysis flow runs as before.
            await expect(page.locator('[data-testid="provider-tab-claude"]')).toHaveAttribute(
                'aria-selected',
                'true',
            );
            await page.getByRole('button', { name: 'Cancel' }).click();
            await expect(page.getByText('Start session')).toBeHidden();

            // Nothing was started and no provider leaked into the config.
            const sc = await sessionCfg(ctrl);
            expect(sc.Provider ?? '').toBe(before.Provider ?? '');
        } finally {
            await setAutoModelRouting(ctrl, false);
        }
    });
});
