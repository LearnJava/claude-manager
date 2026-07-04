import { defineConfig, devices } from '@playwright/test';

// VISUAL=1 turns the run into a watchable walkthrough: a headed browser, slow
// motion between actions, video capture and a screenshot per step. Normal
// (CI/headless) runs are unaffected. Tune the pace with SLOWMO (ms).
const VISUAL = !!process.env.VISUAL;
const SLOWMO = Number(process.env.SLOWMO ?? 500);

// CM_CONTROL_PORT / CM_CONTROL_TOKEN are set by global-setup (or externally).
// The Vite dev server is started automatically by the webServer config below.
export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  retries: process.env.CI ? 2 : 0,
  // Sequential execution prevents shared control-plane events from leaking
  // between tests (e.g. a session:permission overlay blocking sidebar clicks).
  workers: 1,

  // Paths are relative to this config file.
  globalSetup: './tests/global-setup.ts',
  globalTeardown: './tests/global-teardown.ts',

  // Vite dev server: the frontend JS bundle.
  webServer: {
    command: 'npm run dev',
    url: 'http://localhost:5173',
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },

  use: {
    baseURL: 'http://localhost:5173',
    trace: 'on-first-retry',
    screenshot: VISUAL ? 'on' : 'only-on-failure',
    video: VISUAL ? 'on' : 'off',
    headless: VISUAL ? false : undefined,
    launchOptions: { slowMo: VISUAL ? SLOWMO : 0 },
    ignoreHTTPSErrors: true,
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
