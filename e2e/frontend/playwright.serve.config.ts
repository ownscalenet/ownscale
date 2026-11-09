import { defineConfig, devices } from '@playwright/test'
import { baseURL, localTLS } from './tests/connectivity/environment'
import { setupProject } from './tests/support/accounts'

// HTTPS certificates for machines. Only runs where EXTRA_TESTS opts in (see README.md).
export default defineConfig<object, { accountName: string | undefined }>({
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
    setupProject(['dddd']),
    {
      name: 'serve',
      testMatch: /serve\/.*\.spec\.ts/,
      use: {
        ...devices['Desktop Chrome'],
        accountName: 'dddd',
      },
      dependencies: ['setup'],
    },
  ],
})
