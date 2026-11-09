import { defineConfig, devices } from '@playwright/test'
import { localTLS } from './tests/connectivity/environment'
import { namesOf, setupProject } from './tests/support/accounts'

// baseURL matches the dev domain set in compose.dev.yaml ("ownscale.test"),
// with the console served at its "console." subdomain; override with
// E2E_BASE_URL to test another stack.
// Requires `make dev-up` already running (see README.md).
const baseURL = process.env.E2E_BASE_URL ?? 'https://console.ownscale.test'

export default defineConfig({
  testDir: './tests',
  // connectivity/, ssh/, devbox/ and serve/ have their own configs.
  testIgnore: ['**/connectivity/**', '**/ssh/**', '**/devbox/**', '**/serve/**'],
  globalSetup: './run-guard.ts',
  // Each worker runs as its own account in its own organization (see
  // tests/support/account.ts); files stay whole on one worker.
  fullyParallel: false,
  workers: namesOf('worker').length,
  retries: 0,
  maxFailures: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL,
    // The dev stack's TLS cert is signed by ownscale's own local-dev CA
    // (see `make load-cert`); rather than requiring every contributor to
    // trust it, the test client tolerates it there. Remote stacks are validated.
    ignoreHTTPSErrors: localTLS,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    setupProject(namesOf('worker')),
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
      },
      dependencies: ['setup'],
    },
  ],
})
