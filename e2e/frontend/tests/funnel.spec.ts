import { expect, test } from './support/account'
import { registerRealMachine, teardownRealMachine, type RealMachine } from './support/realMachine'

// Creates and deletes a funnel route targeting a real registered device.
// CreateFunnelRoute only validates that the machine/port are well-formed --
// it doesn't probe reachability -- so the route lifecycle can be exercised
// without the device actually listening on the target port.

let machine: RealMachine

test('creating and deleting a funnel route', async ({ page, account }) => {
  test.setTimeout(120_000)
  machine = await registerRealMachine(page, account.org)
  try {
    const label = `e2e${Date.now()}`

    await page.goto('/funnel')
    await expect(page.getByRole('heading', { name: 'Funnel', exact: true })).toBeVisible()

    await page.locator('#funnel-label').fill(label)
    await page.locator('#funnel-machine').selectOption({ label: machine.hostname })
    await page.locator('#funnel-port').fill('8080')
    await page.getByRole('button', { name: 'Create' }).click()

    const row = page.getByRole('row').filter({ hasText: machine.hostname })
    // Creating the org's first funnel starts its tsnet client, which may wait
    // up to funnelUpTimeout (45s) for a DERP relay before the route exists.
    await expect(row).toBeVisible({ timeout: 60_000 })
    await expect(row.getByText('8080', { exact: true })).toBeVisible()

    await row.getByRole('button', { name: 'Remove' }).click()
    await expect(row).toBeHidden()
  } finally {
    await teardownRealMachine(machine)
  }
})
