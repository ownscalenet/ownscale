import { defineConfig, devices } from '@playwright/test'

// baseURL matches docker-compose.yml/.env.example's default BASE_DOMAIN
// ("ownscale.test"); override with E2E_BASE_URL if your local .env uses a
// different domain. Requires `make dev-up` already running (see README.md).
const baseURL = process.env.E2E_BASE_URL ?? 'https://ownscale.test'

export default defineConfig({
  testDir: './tests',
  testIgnore: '**/connectivity/**',
  globalSetup: './run-guard.ts',
  fullyParallel: true,
  // The backend under test is a single local dev instance (SQLite +
  // per-request `docker exec` calls into headscale containers), not built
  // to absorb many simultaneous logins/API calls -- keeping worker count
  // low avoids the login/session race that heavy parallel load against
  // that single instance can trigger. Prioritizes reliability over speed.
  workers: process.env.CI ? 2 : 3,
  // Retries make transient network hiccups against the local dev stack
  // non-fatal without masking real bugs -- CI isn't wired up yet (local-only
  // suite), so this only matters for local runs.
  retries: 1,
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
