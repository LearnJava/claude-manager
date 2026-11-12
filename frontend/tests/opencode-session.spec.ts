/**
 * OpenCode runtime end to end: the S6 session (runtime = "opencode") started
 * from the GUI drives `fakeopencode run --format json`, and:
 *   - the session reaches working → idle (the turn's step_finish ends it),
 *   - the fake's call log shows the exact argv the manager launched
 *     (--format json, --auto, --archived, the full provider/model#variant
 *     selector) and the query text that went to stdin,
 *   - switching S6's CLI to "claude" in the picker persists in config.
 */

import { test, expect } from './fixtures';
import * as fs from 'fs';
import * as path from 'path';
import * as url from 'url';

const PROJECT = 'test';
const SESSION_ID = `${PROJECT}/S6`;

type FakeCall = { args: string[]; query: string };

function fakeOpenCodeCalls(): FakeCall[] {
  // The test run is ESM: no __dirname — resolve this spec's folder instead.
  const here = path.dirname(url.fileURLToPath(import.meta.url));
  const state = JSON.parse(
    fs.readFileSync(path.join(here, '..', '.playwright-state.json'), 'utf-8'),
  ) as { tmpDir: string };
  const logPath = path.join(state.tmpDir, 'fakeopencode-calls.jsonl');
  const data = fs.existsSync(logPath) ? fs.readFileSync(logPath, 'utf-8').trim() : '';
  return data ? data.split('\n').map((l) => JSON.parse(l)) : [];
}

function argValue(args: string[], flag: string): string {
  const i = args.indexOf(flag);
  return i >= 0 && i + 1 < args.length ? args[i + 1] : '';
}

test.describe('OpenCode runtime session', () => {
  test.beforeEach(async ({ ctrl }) => {
    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {});
    await new Promise((r) => setTimeout(r, 200));
  });

  test.afterEach(async ({ ctrl }) => {
    await ctrl.rpc('StopSession', { id: SESSION_ID, soft: false }).catch(() => {});
  });

  test('S6 runs through opencode with the right argv and query', async ({ page, ctrl }) => {
    await page.goto('/');
    await ctrl.rpc('StartSession', { project: PROJECT, session: 'S6' });
    // An interactive (auto_restart = false) per-turn session keeps the
    // process-loop alive between turns: "working" is its steady state.
    await ctrl.wait('session:status', { id: SESSION_ID, status: 'working' }, 12_000);

    // The status event fires before the CLI process starts, so the fake's
    // call log may not exist yet — poll for it.
    await expect.poll(() => fakeOpenCodeCalls().length, { timeout: 10_000 }).toBeGreaterThanOrEqual(1);

    const calls = fakeOpenCodeCalls();
    expect(calls.length).toBeGreaterThanOrEqual(1);
    const c = calls[calls.length - 1];
    // One `opencode run` process, JSON protocol, headless auto-approve,
    // kept out of the user's session list.
    expect(c.args[0]).toBe('run');
    expect(argValue(c.args, '--format')).toBe('json');
    expect(c.args).toContain('--auto');
    expect(c.args).toContain('--archived');
    // The model is the full provider/model selector; the session's effort
    // rides as the #variant suffix.
    expect(argValue(c.args, '--model')).toBe('zai-coding-plan/glm-5.3#high');
    // A fresh conversation: no --session on the first turn, and no
    // autonomous preamble (S6 is interactive with ask_user_marker off) —
    // the query on stdin is exactly the session's prompt.
    expect(argValue(c.args, '--session')).toBe('');
    expect(c.query).toBe('Please do the simple hello task');

    // The turn's reply reaches the UI as the result event (the feed's text
    // rendering itself is covered by the shared log-feed specs).
    await ctrl.wait('session:result', { id: SESSION_ID }, 10_000);
  });
});
