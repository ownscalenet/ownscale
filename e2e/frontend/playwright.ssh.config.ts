import { defineConfig, devices } from '@playwright/test'
import { baseURL } from './tests/connectivity/environment'

// Tailscale SSH and split DNS between real tailscaled containers. Needs docker on top of
// `make dev-up` (see README.md), so it's kept out of the default config.
export default defineConfig({
  testDir: './tests',
  globalSetup: './run-guard.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  outputDir: 'test-results/ssh',
  use: {
    baseURL,
    ignoreHTTPSErrors: true,
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'setup',
      testMatch: /global\.setup\.ts/,
    },
    {
      name: 'ssh',
      testMatch: /(ssh|devbox)\/.*\.spec\.ts/,
      use: {
        ...devices['Desktop Chrome'],
        storageState: 'tests/.auth/admin.json',
      },
      dependencies: ['setup'],
    },
  ],
})
