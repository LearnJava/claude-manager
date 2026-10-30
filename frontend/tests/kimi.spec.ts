import { test, expect } from './fixtures';
import { KIMI_MODELS, modelLabelFor, modelsFor } from '../src/lib/models';

test('Kimi catalog uses the six CLI aliases and desktop labels', () => {
    expect(KIMI_MODELS).toEqual([
        { value: 'kimi-code/kimi-for-coding', label: 'K2.8 Preview' },
        { value: 'kimi-code/kimi-for-coding-highspeed', label: 'K2.7 Code Highspeed' },
        { value: 'kimi-code/k3', label: 'K3' },
        { value: 'kimi-code/k3-256k', label: 'K3-256k' },
        { value: 'kimi-code/k3.1-pro', label: 'K3.1 Pro' },
        { value: 'kimi-code/k3.1-pro-highspeed', label: 'K3.1 Pro Highspeed 2x Usage Cost' },
    ]);
    expect(modelsFor('kimi')).toBe(KIMI_MODELS);
    expect(modelLabelFor('kimi', 'kimi-code/k3.1-pro')).toBe('K3.1 Pro');
});

test('Kimi feed shows assistant text with a bullet, no cost and no permission queue', async ({ page }) => {
    await page.goto('/');
    await page.evaluate(() => (window as any).go.main.App.SetSessionRuntime('test/S1', 'kimi'));
    await page.evaluate(() => (window as any).go.main.App.SetSessionModel('test/S1', 'kimi-code/k3.1-pro'));
    await page.reload();
    try {
        await page.locator('[role="button"]', { hasText: 'S1' }).first().click();
        await expect(page.getByText(/no data/)).toBeVisible();
        await expect(page.getByText('Permission Queue', { exact: true })).toHaveCount(0);
        await expect(page.getByTestId('model-select-test/S1').locator('option')).toHaveCount(6);

        await page.evaluate(() => {
            (window as any).__dispatchWailsEvent('session:log', {
                id: 'test/S1',
                entry: {
                    time: new Date().toISOString(),
                    level: 'text',
                    source: 'kimi',
                    message: 'kimi wire text',
                },
            });
        });
        await expect(page.getByText('• kimi wire text', { exact: true })).toBeVisible();

        await page.getByRole('button', { name: 'Settings' }).click();
        const modal = page.getByRole('dialog');
        await modal.getByRole('button', { name: 'Sessions' }).click();
        await modal.getByRole('button', { name: 'S1' }).first().click();
        const modelSelect = modal.locator('label', { hasText: 'Model' }).locator('select').first();
        await expect(modelSelect).toHaveValue('kimi-code/k3.1-pro');
        await expect(modelSelect.locator('option')).toHaveCount(6);
        await expect(modelSelect.locator('option')).toHaveText(KIMI_MODELS.map((m) => m.label));
    } finally {
        await page.evaluate(() => (window as any).go.main.App.SetSessionRuntime('test/S1', 'claude'));
        await page.evaluate(() => (window as any).go.main.App.SetSessionModel('test/S1', 'claude-sonnet-4-6'));
    }
});
