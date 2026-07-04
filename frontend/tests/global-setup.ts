/**
 * Playwright global setup.
 *
 * Builds cmd/fakeclaude, cmd/fakeworker and cmd/playwright-server (if not
 * already built), creates an isolated temp working directory, seeds a git repo
 * for the mixed-programming project, starts a fakeworker, writes a runtime TOML
 * that points claude_path at fakeclaude and a [[worker]] at the fakeworker,
 * spawns playwright-server, reads its token from stdout, and stores everything
 * in .playwright-state.json for tests and teardown.
 *
 * If CM_CONTROL_TOKEN is already set the build/spawn steps are skipped and
 * the existing server is used instead.
 */

import { execSync, spawn, ChildProcess } from 'child_process';
import * as fs from 'fs';
import * as path from 'path';
import * as os from 'os';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const ROOT = path.resolve(__dirname, '../..');
const STATE_FILE = path.join(__dirname, '../.playwright-state.json');
const CM_PORT = process.env.CM_CONTROL_PORT ?? '7334';
const SCENARIOS_DIR = path.join(ROOT, 'testdata', 'scenarios');
const WORKER_SCENARIO = path.join(ROOT, 'testdata', 'worker-scenarios', 'clean-round1.json');

export interface PlaywrightState {
  pid: number;
  token: string;
  port: string;
  cfgPath: string;
  tmpDir: string;
  workerPid?: number;
  mixedEnabled: boolean;
}

export default async function globalSetup(): Promise<void> {
  // If an external server is already running, just validate it.
  if (process.env.CM_CONTROL_TOKEN) {
    process.env.CM_CONTROL_PORT = CM_PORT;
    await assertConnectable(CM_PORT, process.env.CM_CONTROL_TOKEN);
    return;
  }

  // 1. Build fakeclaude, fakeworker, playwright-server.
  const fakeclaudeBin = buildBinary(
    './cmd/fakeclaude',
    path.join(ROOT, 'cmd', 'fakeclaude', exeName('fakeclaude')),
  );
  const fakeworkerBin = buildBinary(
    './cmd/fakeworker',
    path.join(ROOT, 'cmd', 'fakeworker', exeName('fakeworker')),
  );
  const serverBin = buildBinary(
    './cmd/playwright-server',
    path.join(ROOT, 'cmd', 'playwright-server', exeName('playwright-server')),
  );

  // 2. Isolated temp working dir (holds config, state files, and worktrees so
  //    the main repo tree stays clean).
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'cm-playwright-'));

  // 3. Mixed-programming setup: seed a git repo and start a fakeworker. If git
  //    is unavailable the mixed project is skipped and mixed.spec.ts skips too.
  let workerProc: ChildProcess | undefined;
  let workerUrl = '';
  let seedRepo = '';
  const mixedEnabled = hasGit();
  if (mixedEnabled) {
    seedRepo = seedGitRepo(path.join(tmpDir, 'mixedproj'));
    const started = await startFakeworker(fakeworkerBin, WORKER_SCENARIO);
    workerProc = started.proc;
    workerUrl = started.url;
    // The worker's key_env must resolve to a non-empty value in the server env.
    process.env.FAKEWORKER_KEY = 'playwright-fakeworker-key';
  }

  // 4. Write the runtime TOML.
  const cfgPath = writeTestConfig(tmpDir, fakeclaudeBin, {
    mixedEnabled,
    workerUrl,
    seedRepo,
  });

  // 5. Start the server and read the token.
  const { proc, token } = await startServer(serverBin, cfgPath);

  // 6. Persist state for teardown.
  const state: PlaywrightState = {
    pid: proc.pid!,
    token,
    port: CM_PORT,
    cfgPath,
    tmpDir,
    workerPid: workerProc?.pid,
    mixedEnabled,
  };
  fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));

  process.env.CM_CONTROL_TOKEN = token;
  process.env.CM_CONTROL_PORT = CM_PORT;
  process.env.CM_MIXED_ENABLED = mixedEnabled ? '1' : '0';
}

// ---- helpers ----

function exeName(base: string): string {
  return os.platform() === 'win32' ? `${base}.exe` : base;
}

function hasGit(): boolean {
  try {
    execSync('git --version', { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
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

// seedGitRepo creates a git repo at dir containing greet.go in one commit, so
// `git worktree add` and worker FIND anchors have a HEAD to work against. The
// seed file matches testdata/e2e/mixed-clean-round.json.
function seedGitRepo(dir: string): string {
  fs.mkdirSync(dir, { recursive: true });
  const greet =
    'package greet\n\n// Greet returns a greeting for name.\nfunc Greet(name string) string {\n\treturn "TODO"\n}\n';
  fs.writeFileSync(path.join(dir, 'greet.go'), greet);
  const git = (args: string) => execSync(`git ${args}`, { cwd: dir, stdio: 'ignore' });
  git('init -q');
  git('config user.email playwright@test.local');
  git('config user.name playwright');
  git('add -A');
  git('commit -q -m seed');
  return dir;
}

async function startFakeworker(
  bin: string,
  scenarioPath: string,
): Promise<{ proc: ChildProcess; url: string }> {
  const proc = spawn(bin, ['-scenario', scenarioPath, '-addr', '127.0.0.1:0'], {
    cwd: ROOT,
    env: { ...process.env },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  const url = await new Promise<string>((resolve, reject) => {
    const timeout = setTimeout(
      () => reject(new Error('[global-setup] fakeworker startup timeout')),
      15_000,
    );
    let stdout = '';
    proc.stdout?.on('data', (chunk: Buffer) => {
      stdout += chunk.toString();
      const m = stdout.match(/FAKEWORKER_URL=(\S+)/);
      if (m) {
        clearTimeout(timeout);
        resolve(m[1]);
      }
    });
    proc.stderr?.on('data', (chunk: Buffer) => {
      process.stderr.write(`[fakeworker] ${chunk.toString()}`);
    });
    proc.on('error', (err) => {
      clearTimeout(timeout);
      reject(new Error(`[global-setup] fakeworker spawn failed: ${err.message}`));
    });
  });

  return { proc, url };
}

interface MixedOpts {
  mixedEnabled: boolean;
  workerUrl: string;
  seedRepo: string;
}

function writeTestConfig(tmpDir: string, fakeclaudePath: string, mixed: MixedOpts): string {
  // Normalise to forward slashes for TOML string compatibility on Windows.
  const claudePathToml = fakeclaudePath.replace(/\\/g, '/');
  const projectPath = ROOT.replace(/\\/g, '/');
  const cfgPath = path.join(tmpDir, 'playwright-runtime.toml');

  const lines = [
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
    '',
    // S3 maps to the multi-turn scenario, which awaits stdin and therefore
    // stays alive — used by the visual "send a message" walkthrough where slow
    // motion would otherwise outlast the short-lived happy-path session.
    '[[project.session]]',
    'name = "S3"',
    'prompt = "Let us have a multi-turn conversation"',
    'auto_restart = false',
    'model = "claude-sonnet-4-6"',
    'permission_mode = "default"',
  ];

  if (mixed.mixedEnabled) {
    const seedToml = mixed.seedRepo.replace(/\\/g, '/');
    lines.push(
      '',
      '[[project]]',
      'name = "mixedproj"',
      `path = "${seedToml}"`,
      'mixed_programming = true',
      'gates = ["git --version"]',
      '',
      '[[project.session]]',
      'name = "P1"',
      'prompt = "unused in mixed e2e"',
      'auto_restart = false',
      '',
      '[[worker]]',
      'name = "fake"',
      `base_url = "${mixed.workerUrl}"`,
      'model = "fake/model"',
      'key_env = "FAKEWORKER_KEY"',
      'role = "hands"',
      'reasoning_effort = "low"',
    );
  }

  fs.writeFileSync(cfgPath, lines.join('\n'));
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
