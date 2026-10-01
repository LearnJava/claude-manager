/**
 * Queue views — sidebar mini-bar, TaskPanel "Queue" tab, Log | Board toggle.
 *
 * playwright-server has no roadmap on disk, so GetSessionRoadmap is stubbed in
 * the page (after the bridge installs window.go) with a fixed roadmap tree:
 * three rows done, RT-04 current, RT-05..07 queued (RT-06 waits for RT-05).
 * Session test/S1 is left idle; the queue views don't depend on it running.
 */

import { test, expect } from './fixtures';
import type { Page } from '@playwright/test';

const SID = 'test/S1';

function node(id: string, o: Record<string, unknown> = {}) {
  return {
    kind: 'task', id, number: 0, name: `${id} name`, summary: '', status: 'pending', status_raw: '',
    current: false, in_queue: false, detail_path: '', depends_on: '', bugs: '', size: '', line: 1,
    roadmap_file: 'ROADMAP.md', has_detail: false, done: 0, total: 1, ...o,
  };
}

const VIEW = {
  title: 'Runtime roadmap', roadmap_file: 'ROADMAP.md', status_file: 'STATUS-P1.md', context: '',
  status_model: 'curated', done: 3, total: 7,
  nodes: [
    node('RT-01', { status: 'done' }),
    node('RT-02', { status: 'done' }),
    node('RT-03', { status: 'done' }),
    node('RT-04', { in_queue: true, current: true, size: 'M' }),
    node('RT-05', { in_queue: true, size: 'S', depends_on: 'RT-04' }),
    node('RT-06', { in_queue: true, size: 'M', depends_on: 'RT-05' }),
    node('RT-07', { in_queue: true, status: 'blocked' }),
  ],
};

async function stubRoadmap(page: Page, view: unknown) {
  await page.addInitScript((v) => {
    (window as any).go.main.App.GetSessionRoadmap = () => Promise.resolve(v);
  }, view);
}

async function openS1(page: Page) {
  await page.goto('/');
  const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
  await expect(row).toBeVisible({ timeout: 8_000 });
  await row.click();
}

test.describe('Task queue views', () => {
  test('sidebar mini-bar shows exact counts and unfolds the list on click', async ({ page }) => {
    await stubRoadmap(page, VIEW);
    await openS1(page);

    const mini = page.getByTestId(`queue-mini-${SID}`);
    await expect(mini).toBeVisible();
    await expect(mini).toContainText('1 up next');
    await expect(mini).toContainText('3 queued');
    await expect(page.getByTestId(`queue-mini-list-${SID}`)).toHaveCount(0);

    await mini.locator('button').click();
    const list = page.getByTestId(`queue-mini-list-${SID}`);
    await expect(list).toContainText('RT-04');
    await expect(list).toContainText('RT-07');
    // Unfolding must not have deselected/changed anything else: the session view is still there.
    await expect(page.getByTestId('queue-panel')).toBeVisible();
  });

  test('Queue tab is the default and lists current + upcoming with dependencies', async ({ page }) => {
    await stubRoadmap(page, VIEW);
    await openS1(page);

    const panel = page.getByTestId('queue-panel');
    await expect(page.getByTestId('queue-current')).toContainText('RT-04');
    await expect(page.getByTestId('queue-current')).toContainText('Up next');
    const upcoming = page.getByTestId('queue-upcoming');
    await expect(upcoming.locator('li')).toHaveCount(3);
    await expect(upcoming).toContainText('waits for RT-05');
    await expect(upcoming).toContainText('blocked');
    await expect(panel).toContainText('Done in the roadmap: 3');
    await expect(page.getByRole('button', { name: /^Queue · 4$/ })).toBeVisible();
  });

  test('Roadmap tab stays reachable next to Queue', async ({ page }) => {
    await stubRoadmap(page, VIEW);
    await openS1(page);
    await page.getByRole('button', { name: 'Roadmap', exact: true }).click();
    await expect(page.getByTestId('queue-panel')).toHaveCount(0);
    await expect(page.getByText('Runtime roadmap')).toBeVisible();
  });

  test('Board replaces the log; Log is the default and comes back', async ({ page }) => {
    await stubRoadmap(page, VIEW);
    await openS1(page);

    await expect(page.getByTestId('queue-board')).toHaveCount(0);
    await page.getByTestId('view-board').click();

    const board = page.getByTestId('queue-board');
    await expect(board).toBeVisible();
    await expect(page.getByTestId('queue-col-queue')).toContainText('RT-05');
    await expect(page.getByTestId('queue-col-queue')).toContainText('RT-06');
    await expect(page.getByTestId('queue-col-running')).toContainText('RT-04');
    await expect(page.getByTestId('queue-col-blocked')).toContainText('RT-07');
    await expect(page.getByTestId('queue-col-done')).toContainText('RT-03');

    await page.getByTestId('view-log').click();
    await expect(board).toHaveCount(0);
  });

  test('a session without a queue gets no toggle, no Queue tab and no mini-bar', async ({ page }) => {
    await stubRoadmap(page, null);
    await openS1(page);
    await expect(page.getByRole('button', { name: 'Roadmap', exact: true })).toBeVisible();
    await expect(page.getByTestId('view-toggle')).toHaveCount(0);
    await expect(page.getByRole('button', { name: /^Queue/ })).toHaveCount(0);
    await expect(page.getByTestId(`queue-mini-${SID}`)).toHaveCount(0);
  });
});
