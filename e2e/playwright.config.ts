import { defineConfig, devices } from '@playwright/test'

// This suite drives the real docker-compose stack (ownscale + dex + caddy)
// with a real, freshly-migrated SQLite database -- nothing here is mocked.
// See e2e/README.md for prerequisites (Docker, /etc/hosts entries, etc.)
// and e2e/lib/stack.ts for how the stack is reset/started before tests run.
export default defineConfig({
  testDir: './tests',
  // Org provisioning (a real headscale container) and OIDC
  // redirects through a real Dex instance are slow compared to typical
  // frontend e2e tests; give generous timeouts throughout.
  timeout: 90_000,
  expect: { timeout: 15_000 },
  // Tests share one backend/org and DB, so run them serially in a single
  // worker to avoid cross-test interference (e.g. two tests racing to
  // create/rename the same headscale user). A single retry absorbs rare,
  // environment-level hiccups (e.g. a slightly slow real OIDC round trip)
  // without masking genuine failures.
  fullyParallel: false,
  workers: 1,
  retries: 1,
  reporter: [['list']],
  use: {
    baseURL: 'https://ownscale.test',
    // Caddy serves everything from its own local CA in this dev setup;
    // simplest to ignore cert errors in the test browser rather than
    // importing the CA into Playwright's browser trust store.
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
        storageState: '.auth/admin.json',
      },
      dependencies: ['setup'],
      testIgnore: /global\.setup\.ts/,
    },
  ],
})
