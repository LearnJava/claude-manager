/**
 * Playwright global setup.
 *
 * Builds cmd/fakeclaude and cmd/playwright-server (if not already built),
 * writes a test-specific TOML that points claude_path at the fakeclaude
 * binary, spawns playwright-server, reads its token from stdout, and stores
 * everything in .playwright-state.json for tests and teardown.
 *
 * If CM_CONTROL_TOKEN is already set the build/spawn steps are skipped and
 * the existing server is used instead.
 */

import { execSync, spawn, ChildProcess } from 'child_process';
import * as fs from 'fs';
import * as path from 'path';
import * as os from 'os';

const ROOT = path.resolve(__dirname, '../../..');
const STATE_FILE = path.join(__dirname, '../.playwright-state.json');
const CM_PORT = process.env.CM_CONTROL_PORT ?? '7334';
const SCENARIOS_DIR = path.join(ROOT, 'testdata', 'scenarios');

export interface PlaywrightState {
  pid: number;
  token: string;
  port: string;
  cfgPath: string;
}

export default async function globalSetup(): Promise<void> {
  // If an external server is already running, just validate it.
  if (process.env.CM_CONTROL_TOKEN) {
    process.env.CM_CONTROL_PORT = CM_PORT;
    await assertConnectable(CM_PORT, process.env.CM_CONTROL_TOKEN);
    return;
  }

  // 1. Build fakeclaude.
  const fakeclaudeBin = buildBinary(
    './cmd/fakeclaude',
    path.join(ROOT, 'cmd', 'fakeclaude', exeName('fakeclaude')),
  );

  // 2. Build playwright-server.
  const serverBin = buildBinary(
    './cmd/playwright-server',
    path.join(ROOT, 'cmd', 'playwright-server', exeName('playwright-server')),
  );

  // 3. Write test TOML with the absolute fakeclaude path.
  const cfgPath = writeTestConfig(fakeclaudeBin);

  // 4. Start the server and read the token.
  const { proc, token } = await startServer(serverBin, cfgPath);

  // 5. Persist state for teardown.
  const state: PlaywrightState = { pid: proc.pid!, token, port: CM_PORT, cfgPath };
  fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));

  process.env.CM_CONTROL_TOKEN = token;
  process.env.CM_CONTROL_PORT = CM_PORT;
}

// ---- helpers ----

function exeName(base: string): string {
  return os.platform() === 'win32' ? `${base}.exe` : base;
}

function buildBinary(pkg: string, outPath: string): string {
  // Skip rebuild if binary already exists.
  if (fs.existsSync(outPath)) return outPath;

  console.log(`[global-setup] building ${pkg} → ${path.relative(ROOT, outPath)}`);
  execSync(`go build -o "${outPath}" ${pkg}`, {
    cwd: ROOT,
    stdio: 'inherit',
    env: { ...process.env },
  });
  return outPath;
}

function writeTestConfig(fakeclaudePath: string): string {
  // Normalise to forward slashes for TOML string compatibility on Windows.
  const claudePathToml = fakeclaudePath.replace(/\\/g, '/');
  const projectPath = ROOT.replace(/\\/g, '/');
  const cfgPath = path.join(ROOT, 'testdata', 'configs', 'playwright-runtime.toml');

  const toml = [
    '[settings]',
    `claude_path = "${claudePathToml}"`,
    'log_retention_days = 1',
    'crash_recovery = false',
    'preflight_analysis = false',
    'session_start_delay = 0',
    '',
    '[[project]]',
    'name = "test"',
    `path = "${projectPath}"`,
    '',
    '[[project.session]]',
    'name = "S1"',
    'prompt = "Please do the simple hello task"',
    'auto_restart = false',
    'model = "claude-sonnet-4-6"',
    'permission_mode = "default"',
    '',
    '[[project.session]]',
    'name = "S2"',
    'prompt = "Please refactor this code"',
    'auto_restart = false',
    'model = "claude-sonnet-4-6"',
    'permission_mode = "default"',
  ].join('\n');

  fs.writeFileSync(cfgPath, toml);
  return cfgPath;
}

async function startServer(
  serverBin: string,
  cfgPath: string,
): Promise<{ proc: ChildProcess; token: string }> {
  const proc = spawn(
    serverBin,
    ['-config', cfgPath, '-port', CM_PORT, '-scenarios', SCENARIOS_DIR],
    {
      cwd: ROOT,
      env: { ...process.env },
      detached: false,
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );

  const token = await new Promise<string>((resolve, reject) => {
    const timeout = setTimeout(
      () => reject(new Error('[global-setup] playwright-server startup timeout')),
      20_000,
    );

    let stdout = '';
    proc.stdout?.on('data', (chunk: Buffer) => {
      stdout += chunk.toString();
      const m = stdout.match(/CM_CONTROL_TOKEN=([0-9a-f]{64})/);
      if (m) {
        clearTimeout(timeout);
        resolve(m[1]);
      }
    });

    proc.stderr?.on('data', (chunk: Buffer) => {
      process.stderr.write(`[playwright-server] ${chunk.toString()}`);
    });

    proc.on('error', (err) => {
      clearTimeout(timeout);
      reject(new Error(`[global-setup] spawn failed: ${err.message}`));
    });

    proc.on('exit', (code) => {
      clearTimeout(timeout);
      if (code !== null && code !== 0) {
        reject(new Error(`[global-setup] playwright-server exited with code ${code}`));
      }
    });
  });

  return { proc, token };
}

async function assertConnectable(port: string, token: string): Promise<void> {
  const res = await fetch(`http://127.0.0.1:${port}/rpc`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CM-Token': token },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'GetAllSessions', params: {} }),
  });
  if (!res.ok) {
    throw new Error(
      `[global-setup] cannot reach control-plane at 127.0.0.1:${port}: HTTP ${res.status}`,
    );
  }
}
