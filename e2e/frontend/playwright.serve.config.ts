import { defineConfig, devices } from '@playwright/test'
import { baseURL, localTLS } from './tests/connectivity/environment'

// HTTPS certificates for machines. Only runs where EXTRA_TESTS opts in (see README.md).
export default defineConfig({
  testDir: './tests',
  globalSetup: './run-guard.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  outputDir: 'test-results/serve',
  use: {
    baseURL,
    ignoreHTTPSErrors: localTLS,
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'setup',
      testMatch: /global\.setup\.ts/,
    },
    {
      name: 'serve',
      testMatch: /serve\/.*\.spec\.ts/,
      use: {
        ...devices['Desktop Chrome'],
        accountName: 'serve',
      },
      dependencies: ['setup'],
    },
  ],
})
