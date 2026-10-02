import type { Page } from '@playwright/test'
import { expect, test } from './support/account'
import { registerRealMachine, teardownRealMachine } from './support/realMachine'

// openMachines opens the Machines page with filters in a new tab and marks
// the loaded document, so the test can tell a refresh from a reload.
async function openMachines(page: Page, filters: string): Promise<{ tab: Page; reloaded: () => Promise<boolean> }> {
  const tab = await page.context().newPage()
  await tab.goto(`/machines?${filters}`)
  await expect(tab.getByRole('heading', { name: 'Machines' })).toBeVisible()
  await tab.evaluate(() => Object.assign(window, { e2eLoaded: true }))
  return { tab, reloaded: async () => !(await tab.evaluate(() => 'e2eLoaded' in window)) }
}

test('the machines list refreshes on its own and keeps its filters', async ({ page, account }) => {
  test.setTimeout(120_000)
  // Real machines are named mlc<hex> (see support/realMachine.ts).
  const matching = await openMachines(page, 'q=mlc')
  const excluded = await openMachines(page, 'q=mlc&status=offline')

  const machine = await registerRealMachine(page, account.org)
  try {
    const row = (tab: Page) => tab.getByRole('row').filter({ hasText: machine.hostname })
    await expect(row(matching.tab)).toBeVisible({ timeout: 15_000 })
    expect(await matching.reloaded()).toBe(false)
    expect(new URL(matching.tab.url()).search).toBe('?q=mlc')

    // The machine is online, so a refresh of the offline-filtered list must
    // still leave it out.
    await excluded.tab.waitForResponse((r) => new URL(r.url()).pathname === '/api/machines', { timeout: 15_000 })
    await expect(row(excluded.tab)).toBeHidden()
    expect(await excluded.reloaded()).toBe(false)
  } finally {
    await teardownRealMachine(machine)
  }
})
