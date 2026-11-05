import { test, expect } from './fixtures';
import { ZCODE_MODELS, modelLabelFor, modelsFor, capabilitiesOf } from '../src/lib/models';

test('ZCode catalog mirrors the picker names and file-transport capabilities', () => {
    // Names as ZCode's own model picker shows them (Z.ai account, checked
    // 2026-11-05); the value travels verbatim into the order file.
    expect(ZCODE_MODELS).toEqual([
        { value: 'GLM-5.3', label: 'GLM-5.3' },
        { value: 'GLM-5.3-Flash', label: 'GLM-5.3-Flash' },
    ]);
    expect(modelsFor('zcode')).toBe(ZCODE_MODELS);
    // Exact ids: no folding onto the Claude aliases.
    expect(modelLabelFor('zcode', 'GLM-5.3-Flash')).toBe('GLM-5.3-Flash');
    expect(modelLabelFor('zcode', 'claude-sonnet-5')).toBe('claude-sonnet-5');
    // Mirrors zcodeRuntime.Capabilities() (internal/session/zcode_runtime.go).
    expect(capabilitiesOf('zcode')).toEqual({
        resume: true, effort: false, permissionProtocol: true,
        cost: false, tokenUsage: true, images: false, perTurnProcess: true,
    });
});

test('sidebar offers ZCode with its two models', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => (window as any).go.main.App.SetSessionRuntime('test/S1', 'zcode'));
    await page.evaluate(() => (window as any).go.main.App.SetSessionModel('test/S1', 'GLM-5.3'));
    await page.reload();
    await expect(page.getByTestId('runtime-select-test/S1')).toHaveValue('zcode');
    await expect(page.getByTestId('model-select-test/S1')).toHaveValue('GLM-5.3');
    await expect(page.getByTestId('model-select-test/S1').locator('option')).toHaveCount(2);

    await page.locator('[role="button"]', { hasText: 'S1' }).first().click();
    await expect(page.getByTestId('runtime-label')).toHaveText('Use ZCode');
});
