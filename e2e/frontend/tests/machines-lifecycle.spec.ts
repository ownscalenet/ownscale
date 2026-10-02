import { readFile } from 'node:fs/promises'
import { expect, test } from './support/account'
import { registerRealMachine, teardownRealMachine, type RealMachine } from './support/realMachine'

// These tests exercise the Machines page's row-level mutations (rename, tags,
// route-settings empty state, expire, delete, CSV export) against a real
// registered device.

let machine: RealMachine

test('registering a real device and managing it from the Machines page', async ({ page, account }) => {
  test.setTimeout(120_000)
  machine = await registerRealMachine(page, account.org)
  try {
    const { hostname } = machine
    const row = page.getByRole('row').filter({ hasText: hostname })

    // Download the CSV before any mutation, while the machine's still
    // named "hostname", and confirm it's present.
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: 'Download CSV' }).click(),
    ])
    const csvPath = await download.path()
    if (!csvPath) throw new Error('CSV download did not produce a local file path')
    const csv = await readFile(csvPath, 'utf8')
    expect(csv).toContain(hostname)

    // Rename.
    const newName = `${hostname}-renamed`
    await row.getByRole('button', { name: '⋯', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Edit machine name', exact: true }).click()
    const renameDialog = page.locator('dialog.modal[open]')
    await renameDialog.locator('input[type="text"]').fill(newName)
    await renameDialog.getByRole('button', { name: 'Rename', exact: true }).click()
    const renamedRow = page.getByRole('row').filter({ hasText: newName })
    await expect(renamedRow).toBeVisible()

    // Edit ACL tags.
    await renamedRow.getByRole('button', { name: '⋯', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Edit ACL tags', exact: true }).click()
    const tagsDialog = page.locator('dialog.modal[open]')
    await tagsDialog.locator('input[type="text"]').fill('e2e-lifecycle')
    await tagsDialog.getByRole('button', { name: 'Save', exact: true }).click()
    await expect(renamedRow.getByText('tag:e2e-lifecycle', { exact: true })).toBeVisible()

    // Edit route settings: this tsnet-backed device never advertises a
    // subnet route, so the dialog is expected to show its empty state
    // (extending tscl-bridge to advertise routes is out of scope here).
    await renamedRow.getByRole('button', { name: '⋯', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Edit route settings', exact: true }).click()
    const routesDialog = page.locator('dialog.modal[open]')
    await expect(routesDialog.getByText(/hasn.t advertised any routes/)).toBeVisible()
    await routesDialog.getByRole('button', { name: 'Cancel', exact: true }).click()

    // Expire the session.
    page.once('dialog', (d) => void d.accept())
    await renamedRow.getByRole('button', { name: '⋯', exact: true }).click()
    await renamedRow.getByRole('button', { name: 'Expire session', exact: true }).click()
    await expect(renamedRow.getByText('Expired', { exact: true })).toBeVisible()

    // Delete.
    page.once('dialog', (d) => void d.accept())
    await renamedRow.getByRole('button', { name: '⋯', exact: true }).click()
    await renamedRow.getByRole('button', { name: 'Remove', exact: true }).click()
    await expect(renamedRow).toBeHidden()
  } finally {
    await teardownRealMachine(machine)
  }
})
