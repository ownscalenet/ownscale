import { defineConfig, devices } from '@playwright/test'
import { baseURL, localTLS } from './tests/connectivity/environment'
import { setupProject } from './tests/support/accounts'

// Tailscale SSH, split DNS and subnet routing between real tailscaled containers. Needs docker on top of
// `make dev-up` (see README.md), so it's kept out of the default config.
export default defineConfig<object, { accountName: string | undefined }>({
  testDir: './tests',
  globalSetup: './run-guard.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  outputDir: 'test-results/ssh',
  use: {
    baseURL,
    ignoreHTTPSErrors: localTLS,
    trace: 'retain-on-failure',
  },
  projects: [
    setupProject(['hhhh']),
    {
      name: 'ssh',
      testMatch: /(ssh|devbox)\/.*\.spec\.ts/,
      use: {
        ...devices['Desktop Chrome'],
        // Its own account and organization, so it can run alongside the
        // other suites.
        accountName: 'hhhh',
      },
      dependencies: ['setup'],
    },
  ],
})
