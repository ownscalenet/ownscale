import { defineConfig, devices } from '@playwright/test'
import { baseURL } from './tests/connectivity/environment'

export default defineConfig({
  testDir: './tests/connectivity',
  testMatch: ['connectivity.spec.ts', 'tags.spec.ts'],
  globalSetup: './tests/connectivity/preflight.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30 * 60_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],
  outputDir: 'test-results/connectivity',
  use: {
    ...devices['Desktop Chrome'],
    baseURL,
    ignoreHTTPSErrors: true,
    trace: 'off',
    screenshot: 'off',
    video: 'off',
  },
})
