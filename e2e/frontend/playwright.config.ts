import { defineConfig, devices } from '@playwright/test'

// baseURL matches docker-compose.yml/.env.example's default BASE_DOMAIN
// ("ownscale.test"), with the console served at its "console." subdomain;
// override with E2E_BASE_URL if your local .env uses a different domain.
// Requires `make dev-up` already running (see README.md).
const baseURL = process.env.E2E_BASE_URL ?? 'https://console.ownscale.test'

export default defineConfig({
  testDir: './tests',
  // connectivity/, ssh/ and devbox/ have their own configs.
  testIgnore: ['**/connectivity/**', '**/ssh/**', '**/devbox/**'],
  globalSetup: './run-guard.ts',
  fullyParallel: false,
  workers: 1,
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
        storageState: 'tests/.auth/admin.json',
      },
      dependencies: ['setup'],
    },
  ],
})
