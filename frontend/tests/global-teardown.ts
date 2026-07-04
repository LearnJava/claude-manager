import * as fs from 'fs';
import * as path from 'path';
import { fileURLToPath } from 'url';
import type { PlaywrightState } from './global-setup';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const STATE_FILE = path.join(__dirname, '../.playwright-state.json');

export default async function globalTeardown(): Promise<void> {
  if (!fs.existsSync(STATE_FILE)) return;

  let state: PlaywrightState;
  try {
    state = JSON.parse(fs.readFileSync(STATE_FILE, 'utf-8'));
  } catch {
    return;
  }

  for (const pid of [state.pid, state.workerPid]) {
    if (pid) {
      try {
        process.kill(pid, 'SIGTERM');
      } catch {
        // Process may have already exited.
      }
    }
  }

  // Clean up the isolated temp working dir (config, state, worktrees, seed repo).
  if (state.tmpDir && fs.existsSync(state.tmpDir)) {
    try {
      fs.rmSync(state.tmpDir, { recursive: true, force: true });
    } catch {
      /* ignore */
    }
  } else if (state.cfgPath && fs.existsSync(state.cfgPath)) {
    // Backwards-compatible path for configs written outside a temp dir.
    try { fs.unlinkSync(state.cfgPath); } catch { /* ignore */ }
  }

  try { fs.unlinkSync(STATE_FILE); } catch { /* ignore */ }
}
