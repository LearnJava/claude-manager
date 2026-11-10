/**
 * Provider/model picker consistency (user report 2026-11-10): every session
 * row must show the model list of the brain its next start will actually
 * run, before and after switching the provider.
 *
 *   - a session with NO provider whose model id merely collides with a
 *     provider model (legacy config / CLI echo) must NOT be painted as
 *     that provider — it shows the runtime's own list (Claude aliases, or
 *     the hermes-capable union on plain hermes), because the next start
 *     does not go through the provider's artifacts.
 *   - switching a session to a provider persists provider+model and swaps
 *     the model dropdown to exactly that provider's combos.
 *   - switching back to the runtime clears the provider and restores the
 *     runtime list — and on hermes, clears the --provider flag too.
 *   - a plain-hermes session lists the UNION of every hermes-capable
 *     provider's model×effort combos (GLM 6 + Kimi 9).
 */

import { test, expect } from './fixtures';

const PROJECT = 'test';
const ID = (name: string) => `${PROJECT}/${name}`;

type SessionCfg = { Name?: string; Model?: string; Provider?: string; Effort?: string; Runtime?: string; HermesProvider?: string };
type Cfg = { Projects?: Array<{ Name: string; Sessions?: SessionCfg[] }> };

async function getSession(ctrl: any, name: string): Promise<SessionCfg> {
    const cfg = (await ctrl.rpc('GetConfig', {})) as Cfg;
    const p = cfg.Projects?.find((x) => x.Name === PROJECT);
    const sc = p?.Sessions?.find((s) => s.Name === name);
    if (!sc) throw new Error(`no session ${name}`);
    return sc;
}

async function setSession(ctrl: any, name: string, patch: Record<string, unknown>): Promise<void> {
    const cfg = (await ctrl.rpc('GetConfig', {})) as Cfg;
    const p = cfg.Projects!.find((x) => x.Name === PROJECT)!;
    const sc = p.Sessions!.find((s) => s.Name === name)!;
    Object.assign(sc, patch);
    await ctrl.rpc('UpdateConfig', { config: cfg });
}

const COMBO_LABELS = (prefix: string[], efforts: string[]) =>
    prefix.flatMap((m) => efforts.map((e) => `${m} ${e}`));

const GLM_COMBOS = COMBO_LABELS(['GLM-5.3', 'GLM-5.3-Flash'], ['low', 'high', 'max']);
const KIMI_COMBOS = COMBO_LABELS(['Kimi-for-Coding', 'K3', 'K3.1-Pro'], ['low', 'high', 'max']);
const HERMES_UNION = [...GLM_COMBOS, ...KIMI_COMBOS];
const CLAUDE_ALIASES = ['Haiku', 'Sonnet', 'Opus', 'Fable'];

test.describe('Sidebar provider/model pickers', () => {
    test.beforeEach(async ({ ctrl }) => {
        // S1: plain claude session whose model id collides with a provider
        // model (the exact "wrong models" setup from the user's report).
        await setSession(ctrl, 'S1', { Runtime: '', Provider: '', Model: 'glm-5.3' });
        // S4: plain hermes, no provider, a kimi model id.
        await setSession(ctrl, 'S4', { Runtime: 'hermes', Provider: '', HermesProvider: '', Model: 'kimi-for-coding' });
    });

    test.afterEach(async ({ ctrl }) => {
        await setSession(ctrl, 'S1', { Runtime: '', Provider: '', Model: 'claude-sonnet-4-6', HermesProvider: '' });
        await setSession(ctrl, 'S4', { Runtime: 'hermes', Provider: '', HermesProvider: '', Model: '' });
    });

    test('no provider: a colliding model id is not painted as that provider', async ({ page }) => {
        await page.goto('/');
        const aiSel = page.locator(`[data-testid="runtime-select-${ID('S1')}"]`);
        const modelSel = page.locator(`[data-testid="model-select-${ID('S1')}"]`);
        await expect(aiSel).toHaveValue('claude', { timeout: 5_000 });
        // The claude alias list — NOT the GLM combos: the session runs the
        // claude CLI, which cannot serve glm-5.3 without a provider.
        await expect(modelSel.locator('option')).toHaveText(CLAUDE_ALIASES);
    });

    test('plain hermes: model list is the hermes-capable union (GLM+Kimi)', async ({ page }) => {
        await page.goto('/');
        const aiSel = page.locator(`[data-testid="runtime-select-${ID('S4')}"]`);
        const modelSel = page.locator(`[data-testid="model-select-${ID('S4')}"]`);
        await expect(aiSel).toHaveValue('hermes', { timeout: 5_000 });
        await expect(modelSel.locator('option')).toHaveText(HERMES_UNION);
        // The current kimi model keeps its combo selected (matched by
        // model; S4 carries no effort of its own, so the server default
        // picks the tier).
        await expect(modelSel).toHaveValue(/^kimi-for-coding::(low|high|max)$/);
    });

    test('switching providers swaps the list and persists the choice', async ({ page, ctrl }) => {
        await page.goto('/');
        const aiSel = page.locator(`[data-testid="runtime-select-${ID('S1')}"]`);
        const modelSel = page.locator(`[data-testid="model-select-${ID('S1')}"]`);
        await expect(aiSel).toHaveValue('claude', { timeout: 5_000 });

        // Claude GLM: exactly its six combos, the provider's first model
        // resolves the colliding/foreign current model, and the whole
        // choice persists as the session's stored default.
        await aiSel.selectOption('prov:glm');
        await expect(modelSel.locator('option')).toHaveText(GLM_COMBOS, { timeout: 10_000 });
        await expect
            .poll(async () => {
                const sc = await getSession(ctrl, 'S1');
                return { model: sc.Model, provider: sc.Provider };
            })
            .toEqual({ model: 'glm-5.3', provider: 'glm' });

        // Kimi: its nine combos, first model selected.
        await aiSel.selectOption('prov:kimi');
        await expect(modelSel.locator('option')).toHaveText(KIMI_COMBOS, { timeout: 10_000 });
        await expect
            .poll(async () => {
                const sc = await getSession(ctrl, 'S1');
                return { model: sc.Model, provider: sc.Provider };
            })
            .toEqual({ model: 'kimi-for-coding', provider: 'kimi' });

        // Back to plain Claude: the provider clears and the alias list
        // returns (the pinned foreign model id stays visible, not swapped).
        await aiSel.selectOption('claude');
        await expect
            .poll(async () => (await getSession(ctrl, 'S1')).Provider)
            .toBe('');
        await expect(aiSel).toHaveValue('claude');
    });

    test('hermes session switching providers keeps model and flag in step', async ({ page, ctrl }) => {
        await page.goto('/');
        const aiSel = page.locator(`[data-testid="runtime-select-${ID('S4')}"]`);
        const modelSel = page.locator(`[data-testid="model-select-${ID('S4')}"]`);
        await expect(aiSel).toHaveValue('hermes', { timeout: 5_000 });

        // To GLM: the CLI --provider (hermes_provider_id) rides along.
        await aiSel.selectOption('prov:glm');
        await expect
            .poll(async () => {
                const sc = await getSession(ctrl, 'S4');
                return { model: sc.Model, provider: sc.Provider, hermes: sc.HermesProvider };
            })
            .toEqual({ model: 'glm-5.3', provider: 'glm', hermes: 'zai' });
        await expect(modelSel.locator('option')).toHaveText(GLM_COMBOS, { timeout: 10_000 });

        // Back to plain hermes: the flag clears — the next turn must not
        // keep --provider zai while the UI shows plain Hermes.
        await aiSel.selectOption('hermes');
        await expect
            .poll(async () => {
                const sc = await getSession(ctrl, 'S4');
                return { provider: sc.Provider, hermes: sc.HermesProvider };
            })
            .toEqual({ provider: '', hermes: '' });
        await expect(modelSel.locator('option')).toHaveText(HERMES_UNION, { timeout: 10_000 });
    });
});
