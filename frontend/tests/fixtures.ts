/**
 * Extended Playwright test fixtures.
 *
 * Every test gets:
 *   ctrl  — ControlClient for RPC and event-wait calls against the control-plane
 *   page  — standard Playwright Page with the Wails bridge pre-installed
 *
 * Usage:
 *   import { test, expect } from './fixtures';
 */

import { test as base, expect } from '@playwright/test';
import { ControlClient, clientFromEnv } from './helpers/control';
import { installBridge } from './helpers/bridge';

export interface CustomFixtures {
  ctrl: ControlClient;
}

// First extend: add the ctrl fixture.
// Second extend: override the built-in page fixture to install the bridge.
// Two-step chaining avoids TypeScript type conflicts with built-in fixtures.
export const test = base
  .extend<CustomFixtures>({
    ctrl: async ({}, use) => {
      await use(clientFromEnv());
    },
  })
  .extend({
    page: async ({ page }, use) => {
      const port = process.env.CM_CONTROL_PORT ?? '7334';
      const token = process.env.CM_CONTROL_TOKEN ?? '';
      await installBridge(page, port, token);
      await use(page);
    },
  });

export { expect };
