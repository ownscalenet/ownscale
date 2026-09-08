import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests/connectivity',
  testMatch: '*.contract.spec.ts',
  globalSetup: './tests/connectivity/contract-setup.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30_000,
  reporter: [['list']],
  outputDir: 'test-results/connectivity-contract',
  use: { trace: 'off', screenshot: 'off', video: 'off' },
})
