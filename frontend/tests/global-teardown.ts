import * as fs from 'fs';
import * as path from 'path';
import type { PlaywrightState } from './global-setup';

const STATE_FILE = path.join(__dirname, '../.playwright-state.json');

export default async function globalTeardown(): Promise<void> {
  if (!fs.existsSync(STATE_FILE)) return;

  let state: PlaywrightState;
  try {
    state = JSON.parse(fs.readFileSync(STATE_FILE, 'utf-8'));
  } catch {
    return;
  }

  if (state.pid) {
    try {
      process.kill(state.pid, 'SIGTERM');
    } catch {
      // Process may have already exited.
    }
  }

  // Clean up runtime-generated config.
  if (state.cfgPath && fs.existsSync(state.cfgPath)) {
    try { fs.unlinkSync(state.cfgPath); } catch { /* ignore */ }
  }

  try { fs.unlinkSync(STATE_FILE); } catch { /* ignore */ }
}
