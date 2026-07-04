/**
 * Visual helpers — make a headed Playwright run watchable by a human.
 *
 * These do NOT assert anything; they only annotate the screen so you can SEE
 * each step happen:
 *   - narrate()  paints a caption banner at the top of the page
 *   - spotlight() draws a pulsing outline around the element about to be acted on
 *   - step()     narrate + a short beat so the eye can catch up
 *   - shot()     saves a screenshot into test-results/visual/
 *
 * Enable the watchable mode with the VISUAL env var (see playwright.config.ts),
 * which turns on headed + slowMo + video. Without it these are cheap no-ops
 * visually (the caption still renders but tests run headless).
 *
 * Usage:
 *   import { step, spotlight, shot } from './helpers/visual';
 *   await step(page, 'Starting session S1');
 *   await spotlight(startBtn); await startBtn.click();
 *   await shot(page, 'session-working');
 */

import type { Page, Locator } from '@playwright/test';

const BEAT = Number(process.env.VISUAL_BEAT ?? (process.env.VISUAL ? 900 : 0));

/** Pause so a human can follow along (no-op when not in VISUAL mode). */
export async function beat(ms = BEAT): Promise<void> {
  if (ms > 0) await new Promise((r) => setTimeout(r, ms));
}

/** Paint a caption banner at the top of the viewport describing the step. */
export async function narrate(page: Page, text: string): Promise<void> {
  // eslint-disable-next-line no-console
  console.log(`  ▶ ${text}`);
  await page
    .evaluate((msg) => {
      const ID = '__cm_narrator__';
      let el = document.getElementById(ID);
      if (!el) {
        el = document.createElement('div');
        el.id = ID;
        el.style.cssText = [
          'position:fixed', 'top:0', 'left:0', 'right:0', 'z-index:2147483647',
          'padding:10px 16px', 'font:600 15px/1.4 system-ui,sans-serif',
          'color:#fff', 'background:rgba(17,24,39,0.92)',
          'border-bottom:2px solid #6366f1', 'letter-spacing:.2px',
          'pointer-events:none', 'transition:opacity .2s',
        ].join(';');
        document.body.appendChild(el);
      }
      el.textContent = '● ' + msg;
    }, text)
    .catch(() => {/* page may be navigating */});
}

/** Combine a caption with a beat. Numbered for a readable walkthrough. */
let stepNo = 0;
export function resetSteps(): void { stepNo = 0; }
export async function step(page: Page, text: string): Promise<void> {
  stepNo += 1;
  await narrate(page, `Step ${stepNo}. ${text}`);
  await beat();
}

/** Draw a pulsing outline around a locator and scroll it into view. */
export async function spotlight(target: Locator): Promise<void> {
  await target.scrollIntoViewIfNeeded().catch(() => {});
  await target
    .evaluate((el) => {
      const node = el as HTMLElement;
      const prev = node.style.cssText;
      node.style.outline = '3px solid #f59e0b';
      node.style.outlineOffset = '2px';
      node.style.borderRadius = node.style.borderRadius || '4px';
      node.style.boxShadow = '0 0 0 6px rgba(245,158,11,0.35)';
      node.style.transition = 'box-shadow .15s, outline .15s';
      // Auto-clear so successive spotlights don't accumulate.
      setTimeout(() => { node.style.cssText = prev; }, 1200);
    })
    .catch(() => {/* element may have detached */});
  await beat(Math.min(BEAT, 500));
}

export interface EvidenceRow {
  k: string;
  v: string;
  ok?: boolean; // true → green ✓, false → red ✗, undefined → neutral
}

/**
 * Render a fixed "evidence" panel so the proof is visible on screen, not just
 * in the test log. Use it to show metric snapshots, gate output, applied
 * patches — the things that distinguish "actually working" from "green dot".
 */
export async function evidence(
  page: Page,
  title: string,
  rows: EvidenceRow[],
  verdict?: { text: string; ok: boolean },
): Promise<void> {
  await page
    .evaluate(
      ({ title, rows, verdict }) => {
        const ID = '__cm_evidence__';
        let el = document.getElementById(ID);
        if (!el) {
          el = document.createElement('div');
          el.id = ID;
          el.style.cssText = [
            'position:fixed', 'right:16px', 'bottom:16px', 'z-index:2147483647',
            'width:460px', 'max-height:70vh', 'overflow:auto',
            'padding:14px 16px', 'border-radius:10px',
            'font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace',
            'color:#e5e7eb', 'background:rgba(3,7,18,0.95)',
            'border:1px solid #374151', 'box-shadow:0 10px 40px rgba(0,0,0,0.5)',
            // Never intercept clicks meant for the app underneath (e.g. a modal
            // Close button that sits in the same bottom-right corner).
            'pointer-events:none',
          ].join(';');
          document.body.appendChild(el);
        }
        const rowsHtml = rows
          .map((r) => {
            const mark = r.ok === true ? '✓' : r.ok === false ? '✗' : '·';
            const color = r.ok === true ? '#34d399' : r.ok === false ? '#f87171' : '#9ca3af';
            const esc = (s: string) =>
              s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
            return (
              `<div style="display:flex;gap:8px;padding:2px 0">` +
              `<span style="color:${color};width:12px">${mark}</span>` +
              `<span style="color:#9ca3af;min-width:150px">${esc(r.k)}</span>` +
              `<span style="color:#e5e7eb;white-space:pre-wrap">${esc(r.v)}</span>` +
              `</div>`
            );
          })
          .join('');
        const verdictHtml = verdict
          ? `<div style="margin-top:10px;padding:8px 10px;border-radius:6px;font-weight:700;` +
            `color:#052e16;background:${verdict.ok ? '#34d399' : '#f87171'}">` +
            `${verdict.ok ? 'VERDICT ✓ ' : 'VERDICT ✗ '}${verdict.text}</div>`
          : '';
        el.innerHTML =
          `<div style="font-weight:700;color:#a5b4fc;margin-bottom:8px;` +
          `border-bottom:1px solid #374151;padding-bottom:6px">${title}</div>` +
          rowsHtml +
          verdictHtml;
      },
      { title, rows, verdict },
    )
    .catch(() => {});
  await beat();
}

/** Save a full-page screenshot to test-results/visual/NN-name.png. */
let shotNo = 0;
export async function shot(page: Page, name: string): Promise<void> {
  shotNo += 1;
  const n = String(shotNo).padStart(2, '0');
  await page
    .screenshot({ path: `test-results/visual/${n}-${name}.png`, fullPage: false })
    .catch(() => {/* ignore */});
}
