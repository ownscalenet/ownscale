import { defineConfig, devices } from '@playwright/test'

// baseURL matches the dev BASE_DOMAIN in example.env
// ("ownscale.test"), with the console served at its "console." subdomain;
// override with E2E_BASE_URL if your local .env uses a different domain.
// Requires `make dev-up` already running (see README.md).
const baseURL = process.env.E2E_BASE_URL ?? 'https://console.ownscale.test'

export default defineConfig({
  testDir: './tests',
  // connectivity/, ssh/ and devbox/ have their own configs.
  testIgnore: ['**/connectivity/**', '**/ssh/**', '**/devbox/**'],
  globalSetup: './run-guard.ts',
  // Each worker runs as its own account in its own organization (see
  // tests/support/account.ts); files stay whole on one worker.
  fullyParallel: false,
  workers: 3,
  retries: 0,
  maxFailures: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL,
    // The dev stack's TLS cert is signed by ownscale's own local-dev CA
    // (see `make load-cert`); rather than requiring every contributor to
    // trust it, the test client simply tolerates it.
    ignoreHTTPSErrors: true,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'setup',
      testMatch: /global\.setup\.ts/,
    },
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
      },
      dependencies: ['setup'],
    },
  ],
})
