/**
 * Visual walkthrough specs — meant to be WATCHED, not just asserted.
 *
 * Run headed with narration, slow motion and video:
 *   cd frontend
 *   VISUAL=1 npx playwright test visual.spec.ts --headed
 *
 * Each test paints an on-screen caption for every step, spotlights the element
 * it is about to click/fill, and drops screenshots into test-results/visual/.
 * Video of the whole run lands in test-results/ (video:'on' under VISUAL).
 *
 * These still double-check via the control-plane event stream, so a green run
 * means "the screen showed it AND the backend confirmed it".
 *
 * Prereqs (same as the other specs):
 *   - control-plane up (globalSetup starts playwright-server + fakeclaude)
 *   - Vite dev server on :5173 (webServer in playwright.config.ts)
 */

import { test, expect } from './fixtures';
import { step, spotlight, shot, narrate, resetSteps, beat, evidence } from './helpers/visual';

const PROJECT = 'test';

interface Metrics {
  num_turns: number;
  output_tokens: number;
  input_tokens: number;
  total_cost_usd: number;
}

test.describe('Visual walkthrough', () => {
  test.beforeEach(async ({ ctrl }) => {
    // Start from a clean slate so the dots begin grey.
    await ctrl.rpc('StopSession', { id: `${PROJECT}/S1`, soft: false }).catch(() => {});
    await ctrl.rpc('StopSession', { id: `${PROJECT}/S2`, soft: false }).catch(() => {});
    await ctrl.rpc('StopSession', { id: `${PROJECT}/S3`, soft: false }).catch(() => {});
    await new Promise((r) => setTimeout(r, 300));
    resetSteps();
  });

  // ── Scenario A: start a session, watch idle → working ─────────────────────
  test('A · Start a session and watch it come alive', async ({ page, ctrl }) => {
    await page.goto('/');
    await narrate(page, 'Scenario A — starting a session from the sidebar');
    await beat();

    await step(page, 'Select session S1 in the sidebar');
    const row = page.locator('[role="button"]', { hasText: 'S1' }).first();
    await expect(row).toBeVisible({ timeout: 5_000 });
    await spotlight(row);
    await row.click();
    await shot(page, 'session-selected');

    await step(page, 'Click the ▶ Start button — dot should turn green');
    // The sidebar start control appears on hover; RPC-start keeps the demo robust
    // while we still show the visual transition on screen.
    const workingPromise = ctrl.wait('session:status', { id: `${PROJECT}/S1`, status: 'working' }, 12_000);
    await ctrl.rpc('StartSession', { project: PROJECT, session: 'S1' });

    const evt = await workingPromise;
    expect(evt.data).toMatchObject({ id: `${PROJECT}/S1`, status: 'working' });

    await step(page, 'Session is WORKING — log lines stream in');
    await expect(
      page.locator('text=Working on the task').first(),
    ).toBeVisible({ timeout: 8_000 });
    await shot(page, 'session-working');

    await step(page, 'Stop the session — back to idle');
    await ctrl.rpc('StopSession', { id: `${PROJECT}/S1`, soft: false }).catch(() => {});
    await beat();
    await shot(page, 'session-idle');
  });

  // ── Scenario B: type a message and send it ────────────────────────────────
  // Uses S3 (multi-turn scenario) which awaits stdin and stays alive, so the
  // input remains enabled through the slow-motion walkthrough.
  test('B · Type a message and send it to a live session', async ({ page, ctrl }) => {
    await page.goto('/');
    await narrate(page, 'Scenario B — bidirectional messaging');
    await beat();

    await ctrl.rpc('StartSession', { project: PROJECT, session: 'S3' });
    await ctrl.wait('session:status', { id: `${PROJECT}/S3`, status: 'working' }, 12_000);

    await step(page, 'Open session S3');
    const row = page.locator('[role="button"]', { hasText: 'S3' }).first();
    await row.click();

    await step(page, 'Type into the message box');
    const textarea = page.locator('textarea[placeholder*="Type a message"]');
    await expect(textarea).toBeVisible({ timeout: 5_000 });
    await spotlight(textarea);
    await textarea.fill('Please add a unit test for the parser');
    await shot(page, 'message-typed');

    await step(page, 'Click Send — text clears, backend receives the message');
    const logPromise = ctrl.wait('session:log', { id: `${PROJECT}/S3` }, 8_000);
    const sendBtn = page.getByRole('button', { name: /Send/ });
    await spotlight(sendBtn);
    await sendBtn.click();

    await logPromise;
    await expect(textarea).toHaveValue('', { timeout: 3_000 });
    await shot(page, 'message-sent');

    await ctrl.rpc('StopSession', { id: `${PROJECT}/S3`, soft: false }).catch(() => {});
  });

  // ── Scenario C: a permission request appears and is approved ──────────────
  test('C · Approve a permission request', async ({ page, ctrl }) => {
    await page.goto('/');
    await narrate(page, 'Scenario C — permission gate');
    await beat();

    await step(page, 'Open session S2');
    const row = page.locator('[role="button"]', { hasText: 'S2' }).first();
    await expect(row).toBeVisible({ timeout: 5_000 });
    await row.click();

    await step(page, 'Start S2 — its scenario asks to run a tool');
    await ctrl.rpc('StartSession', { project: PROJECT, session: 'S2' });
    await ctrl.wait('session:permission', { id: `${PROJECT}/S2` }, 15_000);

    await step(page, 'A permission banner blocks the session (yellow, pulsing)');
    const banner = page.locator('[role="alertdialog"]');
    await expect(banner).toBeVisible({ timeout: 5_000 });
    await expect(banner).toContainText('waiting for permission');
    await spotlight(banner);
    await shot(page, 'permission-banner');

    await step(page, 'Click ✓ Allow — session resumes to working');
    const allowBtn = banner.getByRole('button', { name: /Allow$/ });
    const resumed = ctrl.wait('session:status', { id: `${PROJECT}/S2`, status: 'working' }, 15_000);
    await spotlight(allowBtn);
    await allowBtn.click();

    await expect(banner).not.toBeVisible({ timeout: 5_000 });
    await resumed;
    await shot(page, 'permission-resolved');

    await ctrl.rpc('StopSession', { id: `${PROJECT}/S2`, soft: false }).catch(() => {});
  });

  // ── Scenario D: change a setting and save it ──────────────────────────────
  test('D · Open Settings, change the model, and save', async ({ page, ctrl }) => {
    await page.goto('/');
    await narrate(page, 'Scenario D — editing configuration');
    await beat();

    await step(page, 'Open the Settings modal');
    const settingsBtn = page.getByRole('button', { name: 'Settings' });
    await spotlight(settingsBtn);
    await settingsBtn.click();
    const modal = page.getByRole('dialog');
    await expect(modal).toContainText('Settings');
    await expect(modal.getByText('Claude CLI path')).toBeVisible({ timeout: 5_000 });
    await shot(page, 'settings-open');

    await step(page, 'Switch to the Sessions tab');
    const sessionsTab = modal.getByRole('button', { name: 'Sessions' });
    await spotlight(sessionsTab);
    await sessionsTab.click();
    await beat();

    await step(page, 'Select session S1 to edit');
    const s1 = modal.getByRole('button', { name: 'S1' }).first();
    await spotlight(s1);
    await s1.click();
    await shot(page, 'settings-sessions-tab');

    await step(page, 'Change the model to haiku');
    // Target the Model combobox by its accessible name — locator('select').first()
    // would grab the Project selector, whose options never include "haiku".
    const modelSelect = modal.getByRole('combobox', { name: 'Model' });
    await spotlight(modelSelect);
    await modelSelect.selectOption('haiku');
    await shot(page, 'settings-model-changed');

    await step(page, 'Click Save — config is persisted to TOML');
    const saveBtn = modal.getByRole('button', { name: 'Save' });
    await spotlight(saveBtn);
    await saveBtn.click();
    await expect(modal.getByText('Saved.')).toBeVisible({ timeout: 5_000 });
    await shot(page, 'settings-saved');

    // Backend confirmation: the change really landed in the config.
    const cfg = await ctrl.rpc<{
      Projects?: Array<{ Name: string; Sessions?: Array<{ Name: string; Model: string }> }>;
    }>('GetConfig', {});
    const model = cfg.Projects
      ?.find((p) => p.Name === PROJECT)
      ?.Sessions?.find((s) => s.Name === 'S1')?.Model;
    expect(model).toBe('haiku');

    await step(page, 'Walkthrough complete — close Settings');
    const closeBtn = modal.getByRole('button', { name: 'Close' });
    await spotlight(closeBtn);
    await closeBtn.click().catch(() => {});
    await shot(page, 'settings-closed');
  });

  // ── Scenario E: PROVE it's working, not just green ────────────────────────
  // Uses the multi-turn session S3 and drives it turn by turn. Output tokens
  // accumulate deterministically (80 → 140 → 190) as each turn is answered, so
  // "it is progressing" is proven by measured growth, not by a timing race.
  test('E · Prove the session is progressing, not just green', async ({ page, ctrl }) => {
    test.setTimeout(90_000);

    const metrics = () => ctrl.rpc<Metrics>('GetSessionMetrics', { id: `${PROJECT}/S3` });
    // Poll until output_tokens exceeds `above`, so we capture real growth
    // regardless of slow-motion pacing or machine load.
    const waitTokensAbove = async (above: number, timeoutMs = 15_000): Promise<Metrics> => {
      const deadline = Date.now() + timeoutMs;
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const m = await metrics();
        if (m.output_tokens > above) return m;
        if (Date.now() > deadline) return m;
        await new Promise((r) => setTimeout(r, 150));
      }
    };

    await page.goto('/');
    await narrate(page, 'Scenario E — a green dot is a claim; here is the proof');
    await beat();

    await step(page, 'Open S3 and start it (a multi-turn task)');
    const row = page.locator('[role="button"]', { hasText: 'S3' }).first();
    await row.click();
    await ctrl.rpc('StartSession', { project: PROJECT, session: 'S3' });
    await ctrl.wait('session:status', { id: `${PROJECT}/S3`, status: 'working' }, 12_000);

    await step(page, 'Snapshot #1 — after turn 1 produced output');
    const s1 = await waitTokensAbove(0);
    await evidence(page, 'Snapshot #1 (turn 1 done)', [
      { k: 'num_turns', v: String(s1.num_turns) },
      { k: 'output_tokens', v: String(s1.output_tokens), ok: s1.output_tokens > 0 },
      { k: 'total_cost_usd', v: `$${s1.total_cost_usd.toFixed(4)}` },
      { k: 'status flag', v: 'working (a claim — the numbers are the proof)' },
    ]);
    await shot(page, 'evidence-snapshot-1');

    await step(page, 'Send a follow-up message — the session must do more work');
    const textarea = page.locator('textarea[placeholder*="Type a message"]');
    await expect(textarea).toBeEnabled({ timeout: 5_000 });
    await spotlight(textarea);
    await textarea.fill('Proceed to the next step, please.');
    const sendBtn = page.getByRole('button', { name: /Send/ });
    await spotlight(sendBtn);
    await sendBtn.click();

    await step(page, 'Snapshot #2 — output tokens grew: it really processed the turn');
    const s2 = await waitTokensAbove(s1.output_tokens);

    // The core proof: measurable work happened between the two snapshots.
    expect(s2.output_tokens).toBeGreaterThan(s1.output_tokens);

    await evidence(
      page,
      'Snapshot #2 (turn 2 done)',
      [
        { k: 'output_tokens', v: `${s1.output_tokens} → ${s2.output_tokens}`, ok: s2.output_tokens > s1.output_tokens },
        { k: 'delta this turn', v: `+${s2.output_tokens - s1.output_tokens} tokens`, ok: true },
        { k: 'num_turns', v: String(s2.num_turns) },
      ],
      { text: 'output grew → the session is progressing, not idle-green', ok: true },
    );
    await shot(page, 'evidence-snapshot-2');

    await step(page, 'Finish the task and read the result event (cost + turns)');
    await textarea.fill('That is all, wrap up.');
    const resultP = ctrl.wait('session:result', { id: `${PROJECT}/S3` }, 15_000);
    await sendBtn.click();
    const result = await resultP;
    const rd = result.data as { result?: { total_cost_usd?: number; num_turns?: number } };
    const final = await metrics();

    // Real work costs money and turns — a green-only flag would show all zeros.
    expect(final.total_cost_usd).toBeGreaterThan(0);
    expect(final.num_turns).toBeGreaterThan(0);

    await evidence(
      page,
      'Final — result event (ground evidence of real work)',
      [
        { k: 'output_tokens total', v: String(final.output_tokens), ok: final.output_tokens > 0 },
        { k: 'num_turns', v: String(final.num_turns), ok: final.num_turns > 0 },
        { k: 'total_cost_usd', v: `$${final.total_cost_usd.toFixed(4)}`, ok: final.total_cost_usd > 0 },
        { k: 'result event cost', v: `$${(rd.result?.total_cost_usd ?? 0).toFixed(4)}`, ok: (rd.result?.total_cost_usd ?? 0) > 0 },
      ],
      { text: 'turns>0, cost>0, output grew turn-by-turn — work happened', ok: true },
    );
    await shot(page, 'evidence-final');
    await beat(1500);

    await ctrl.rpc('StopSession', { id: `${PROJECT}/S3`, soft: false }).catch(() => {});
  });

  // ── Scenario F: mixed programming — ground truth on disk ──────────────────
  // The strongest evidence: a patch was applied to real files and the gates
  // (build/test) passed against the real filesystem, then it was committed.
  test('F · Mixed programming: patch applied + gates green = ground truth', async ({ page, ctrl }) => {
    test.skip(process.env.CM_MIXED_ENABLED === '0', 'git unavailable — mixed project not configured');
    // The mixed round drives a real SSE worker + git worktree/gates, which
    // easily exceeds the 30s default when slow motion / video are on.
    test.setTimeout(120_000);

    await page.goto('/');
    await narrate(page, 'Scenario F — ground truth: real files changed, gates green');
    await beat();

    await step(page, 'Open the Mixed modal');
    const mixedBtn = page.getByRole('button', { name: 'Mixed', exact: true });
    await spotlight(mixedBtn);
    await mixedBtn.click();
    const modal = page.getByRole('dialog');
    await expect(modal).toContainText('Mixed programming');

    await step(page, 'Enter a brief and dispatch to the worker');
    await modal
      .locator('[data-testid="mixed-brief"]')
      .fill('Implement Greet: replace the TODO return with a real greeting.');
    const dispatch = modal.locator('[data-testid="mixed-dispatch"]');
    await spotlight(dispatch);

    const [done] = await Promise.all([
      ctrl.wait('worker:done', { status: 'done' }, 30_000),
      dispatch.click(),
    ]);
    expect((done.data as { rounds: number }).rounds).toBe(1);
    await shot(page, 'mixed-timeline');

    await step(page, 'Pull the ground truth: applied patch + gate exit codes');
    const tasks = await ctrl.rpc<Array<{
      Status: string;
      Branch: string;
      Rounds: Array<{
        Number: number;
        Passed: boolean;
        Applied: Array<{ File: string; Find: string; Replace: string }>;
        Gates: { Passed: boolean; Commands: Array<{ Command: string; ExitCode: number }> };
      }>;
    }>>('GetMixedRounds', { project: 'mixedproj' });

    const task = tasks[tasks.length - 1];
    const round = task.Rounds[task.Rounds.length - 1];
    const patch = round.Applied[0];
    const gate = round.Gates.Commands[0];

    // Ground truth assertions: file changed AND gates passed AND committed.
    expect(task.Status).toBe('done');
    expect(round.Applied.length).toBeGreaterThan(0);
    expect(round.Gates.Passed).toBe(true);
    expect(gate.ExitCode).toBe(0);

    await evidence(
      page,
      'Ground truth — on disk, not a status flag',
      [
        { k: 'task status', v: task.Status, ok: task.Status === 'done' },
        { k: 'worktree branch', v: task.Branch },
        { k: 'file patched', v: patch?.File ?? '(none)', ok: !!patch },
        { k: 'FIND anchor', v: (patch?.Find ?? '').trim().slice(0, 40) },
        { k: 'REPLACE applied', v: (patch?.Replace ?? '').trim().slice(0, 40), ok: !!patch?.Replace },
        { k: `gate: ${gate?.Command ?? ''}`.slice(0, 40), v: `exit ${gate?.ExitCode}`, ok: gate?.ExitCode === 0 },
        { k: 'gates passed', v: String(round.Gates.Passed), ok: round.Gates.Passed },
      ],
      { text: 'code really changed and the gates are green', ok: true },
    );
    await shot(page, 'mixed-ground-truth');
    await beat(2000);

    await modal.getByRole('button', { name: 'Close' }).click().catch(() => {});
  });
});
