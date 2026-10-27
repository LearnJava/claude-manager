/**
 * "Waiting for you" screen — WASTE-TASKS.md WS-08.
 *
 * The first half pins the pure model (src/lib/waiting.ts): which session state
 * becomes which row, in which order. The second half drives the real UI with
 * GetConfig / GetAllSessions / GetSessionRoadmap / GetMixedRounds stubbed in the
 * page (after the bridge installs window.go), so every kind of wait is on screen
 * without a real stuck session.
 */

import { test, expect } from './fixtures';
import { test as unit, expect as uexpect } from '@playwright/test';
import type { Page } from '@playwright/test';
import { buildWaiting, queueEmptied, type WaitingContext } from '../src/lib/waiting';
import type { SessionState } from '../src/stores/sessions';
import { EMPTY_QUEUE, type QueueModel } from '../src/lib/queue';

const NOW = Date.parse('2026-10-27T12:00:00Z');
const ago = (sec: number) => new Date(NOW - sec * 1000).toISOString();
const ahead = (sec: number) => new Date(NOW + sec * 1000).toISOString();

function sess(name: string, o: Partial<SessionState> = {}): SessionState {
  return {
    id: `test/${name}`, project: 'test', name, status: 'idle', model: '', effort: '', permission_mode: '',
    started_at: '', last_activity: ago(60), rate_limit_until: '', tasks_done: 0, current_task: '',
    task_source_description: '', prompt: '', todos: [], branch: '', cli_session_id: '',
    stop_requested: false, pending_permission: null, pending_question: null,
    input_tokens: 0, output_tokens: 0, cache_read: 0, cache_creation: 0, num_turns: 0,
    total_cost_usd: 0, context_window: 0, context_util: 0, ...o,
  } as SessionState;
}

const DRAINED: QueueModel = { ...EMPTY_QUEUE, doneCount: 3, total: 3 };
const PENDING: QueueModel = {
  ...EMPTY_QUEUE, doneCount: 1, total: 2,
  current: { id: 'A-2', name: 'next', summary: '', size: '', status: 'pending', current: true, dependsOn: [], waitsFor: [] },
};
const ctx = (o: Partial<WaitingContext> = {}): WaitingContext => ({ queues: {}, errors: {}, mixed: [], ...o });
const kinds = (list: SessionState[], c: WaitingContext = ctx()) => buildWaiting(list, c).map((i) => `${i.kind}:${i.sessionId || i.taskId}`);

unit.describe('buildWaiting', () => {
  unit('every kind of wait becomes a row', () => {
    const list = [
      sess('perm', { status: 'waiting_permission', pending_permission: { tool: 'Bash', command: 'rm -rf x', waiting_since: ago(30) } }),
      sess('ask', { status: 'waiting_for_user', pending_question: { id: 'q', question: 'Which db?', asked_at: ago(20) } }),
      sess('gates', { status: 'error' }),
      sess('err', { status: 'error' }),
      sess('rate', { status: 'rate_limited', rate_limit_until: ahead(600) }),
      sess('stop', { status: 'idle', stopped_after_task_at: ago(90) }),
      sess('empty', { status: 'idle', tasks_done: 3 }),
    ];
    const c = ctx({
      errors: { 'test/gates': 'gates still red after 3 rework rounds — stopped', 'test/err': 'boom' },
      queues: { 'test/empty': { model: DRAINED } },
      mixed: [{ ID: 'test/b1/w', Project: 'test', WorkerName: 'qwen', Error: 'stuck' }],
    });
    uexpect(kinds(list, c)).toEqual([
      'permission:test/perm',
      'question:test/ask',
      'gates_exhausted:test/gates',
      'error:test/err',
      'needs_human:test/b1/w',
      'rate_limit:test/rate',
      'stopped:test/stop',
      'queue_empty:test/empty',
    ]);
    const perm = buildWaiting(list, c)[0];
    uexpect(perm.detail).toBe('Bash: rm -rf x');
    uexpect(perm.since).toBe(NOW - 30_000);
  });

  unit('a working, starting or healthy idle session is not a row', () => {
    const list = [
      sess('w', { status: 'working' }),
      sess('s', { status: 'starting' }),
      sess('r', { status: 'retrying' }),
      sess('i', { status: 'idle' }),
      sess('stopping', { status: 'stopping' }),
    ];
    uexpect(kinds(list)).toEqual([]);
  });

  unit('queue_empty needs tasks done, finished rows and nothing ahead', () => {
    const s = sess('q', { tasks_done: 2 });
    uexpect(queueEmptied(s, { model: DRAINED })).toBe(true);
    uexpect(queueEmptied(s, { model: PENDING })).toBe(false);
    uexpect(queueEmptied(s, { model: EMPTY_QUEUE })).toBe(false);
    uexpect(queueEmptied(s, undefined)).toBe(false);
    uexpect(queueEmptied(sess('q0', { tasks_done: 0 }), { model: DRAINED })).toBe(false);
  });

  unit('a stopped session with an empty queue is one row, the stop wins', () => {
    const s = sess('x', { tasks_done: 1, stopped_after_task_at: ago(5) });
    uexpect(kinds([s], ctx({ queues: { 'test/x': { model: DRAINED } } }))).toEqual(['stopped:test/x']);
  });

  unit('same kind: the longest wait first; unknown time last', () => {
    const list = [
      sess('late', { status: 'waiting_for_user', pending_question: { id: '1', question: 'a', asked_at: ago(10) } }),
      sess('none', { status: 'waiting_for_user', pending_question: { id: '2', question: 'b' } }),
      sess('early', { status: 'waiting_for_user', pending_question: { id: '3', question: 'c', asked_at: ago(500) } }),
    ];
    uexpect(kinds(list)).toEqual(['question:test/early', 'question:test/late', 'question:test/none']);
  });

  unit('an error without a recorded message is a plain error', () => {
    uexpect(kinds([sess('e', { status: 'error' })])).toEqual(['error:test/e']);
  });
});

// ---- UI ----

const ALL_DONE_VIEW = {
  title: 'R', roadmap_file: 'ROADMAP.md', status_file: 'STATUS-P1.md', context: '', status_model: 'curated',
  done: 1, total: 1,
  nodes: [{
    kind: 'task', id: 'T-1', number: 0, name: 'T-1 name', summary: '', status: 'done', status_raw: '',
    current: false, in_queue: false, detail_path: '', depends_on: '', bugs: '', size: '', line: 1,
    roadmap_file: 'ROADMAP.md', has_detail: false, done: 1, total: 1,
  }],
};

// The page runs on the real clock, so the UI fixtures are relative to it.
const wall = (sec: number) => new Date(Date.now() + sec * 1000).toISOString();
const UI_SESSIONS = [
  sess('busy', { status: 'working' }),
  sess('perm', { status: 'waiting_permission', pending_permission: { request_id: 'r1', tool: 'Edit', file: 'a.go', waiting_since: wall(-30) } }),
  sess('ask', { status: 'waiting_for_user', pending_question: { id: 'q', question: 'Which db?', asked_at: wall(-20) } }),
  sess('err', { status: 'error' }),
  sess('rate', { status: 'rate_limited', rate_limit_until: wall(600) }),
  sess('stop', { status: 'idle', stopped_after_task_at: wall(-90) }),
  sess('drained', { status: 'idle', tasks_done: 2 }),
];
const UI_KINDS = ['permission', 'question', 'error', 'needs_human', 'rate_limit', 'stopped', 'queue_empty'];

async function stub(page: Page, o: { enabled: boolean; sessions: SessionState[]; mixed?: boolean }) {
  await page.addInitScript(({ enabled, sessions, view, mixed }) => {
    const app = (window as any).go.main.App;
    const realConfig = app.GetConfig;
    app.GetConfig = () => realConfig().then((c: any) => ({ ...c, Optimization: { ...(c?.Optimization ?? {}), WaitingScreen: enabled } }));
    app.GetAllSessions = () => Promise.resolve(sessions);
    app.GetSessionRoadmap = (_p: string, name: string) => Promise.resolve(name === 'drained' ? view : null);
    app.GetMixedRounds = (project: string) => Promise.resolve(project !== 'test' || !mixed ? [] : [
      { ID: 'test/b1/w', Project: 'test', WorkerName: 'qwen', Status: 'needs_human', Error: 'stuck' },
      { ID: 'test/b2/w', Project: 'test', WorkerName: 'qwen', Status: 'done', Error: '' },
    ]);
  }, { enabled: o.enabled, sessions: o.sessions, view: ALL_DONE_VIEW, mixed: o.mixed ?? false });
  await page.goto('/');
}

test.describe('Waiting screen', () => {
  test('off by default behaviour: no button, status bar as before', async ({ page }) => {
    await stub(page, { enabled: false, sessions: UI_SESSIONS, mixed: true });
    await expect(page.getByText('Waiting: 2')).toBeVisible({ timeout: 8_000 });
    await expect(page.getByTestId('open-waiting')).toHaveCount(0);
    await expect(page.getByTestId('waiting-overlay')).toHaveCount(0);
  });

  test('a row for every kind of wait, with reason and counter', async ({ page }) => {
    await stub(page, { enabled: true, sessions: UI_SESSIONS, mixed: true });
    const btn = page.getByTestId('open-waiting');
    await expect(btn).toContainText('Waiting for you: 7', { timeout: 8_000 });
    await btn.click();

    const rows = page.getByTestId('waiting-row');
    await expect(rows).toHaveCount(7);
    expect(await rows.evaluateAll((els) => els.map((e) => e.getAttribute('data-kind')))).toEqual(UI_KINDS);
    await expect(rows.nth(0)).toContainText('Waiting for a permission');
    await expect(rows.nth(0)).toContainText('Edit: a.go');
    await expect(rows.nth(1)).toContainText('Which db?');
    await expect(rows.nth(4)).toContainText('resumes in');
    // The permission queue still opens from its own place in the status bar.
    await expect(page.getByText('Waiting: 2')).toBeVisible();
  });

  test('nothing waiting: a plain placeholder, the button still opens it', async ({ page }) => {
    await stub(page, { enabled: true, sessions: [sess('busy', { status: 'working' })] });
    const btn = page.getByTestId('open-waiting');
    await expect(btn).toContainText('Waiting for you: 0', { timeout: 8_000 });
    await btn.click();
    await expect(page.getByTestId('waiting-empty')).toContainText('Nothing is waiting for you');
    await expect(page.getByTestId('waiting-row')).toHaveCount(0);
  });

  test('Escape, the backdrop and ✕ close it; a click inside does not', async ({ page }) => {
    await stub(page, { enabled: true, sessions: UI_SESSIONS, mixed: true });
    const overlay = page.getByTestId('waiting-overlay');
    const open = async () => {
      await page.getByTestId('open-waiting').click();
      await expect(overlay).toBeVisible();
    };

    await open();
    await page.keyboard.press('Escape');
    await expect(overlay).toHaveCount(0);

    await open();
    await page.getByTestId('waiting-screen').click({ position: { x: 5, y: 5 } });
    await expect(overlay).toBeVisible();
    await overlay.click({ position: { x: 5, y: 5 } });
    await expect(overlay).toHaveCount(0);

    await open();
    await page.getByTestId('waiting-close').click();
    await expect(overlay).toHaveCount(0);
  });

  test('"Go to session" selects it and closes; "Answer…" opens the permission queue', async ({ page }) => {
    await stub(page, { enabled: true, sessions: UI_SESSIONS, mixed: true });
    await page.getByTestId('open-waiting').click();
    const rows = page.getByTestId('waiting-row');

    await rows.nth(1).getByTestId('waiting-go').click();
    await expect(page.getByTestId('waiting-overlay')).toHaveCount(0);
    await expect(page.getByText('Select a session from the sidebar')).toHaveCount(0);

    await page.getByTestId('open-waiting').click();
    await rows.nth(0).getByTestId('waiting-answer').click();
    await expect(page.getByTestId('waiting-overlay')).toHaveCount(0);
    await expect(page.getByRole('dialog').getByText('Permission Queue')).toBeVisible();
  });
});
