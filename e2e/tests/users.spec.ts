import { test, expect } from '@playwright/test'
import { ADMIN_EMAIL } from '../lib/dex'

test('lists the admin as owner, and creates/renames/deletes a headscale user', async ({ page }) => {
  await page.goto('/users')

  await expect(page.getByRole('heading', { name: 'Users', exact: true })).toBeVisible()

  const membersTable = page.locator('table').first()
  await expect(membersTable.getByText(ADMIN_EMAIL)).toBeVisible()
  await expect(membersTable.getByText('Owner', { exact: true })).toBeVisible()

  // Create a headscale user.
  const name = `e2e-hsuser-${Date.now()}`
  await page.getByRole('button', { name: 'Create user' }).click()
  const createDialog = page.locator('dialog', { hasText: 'Create a headscale user' })
  await createDialog.getByPlaceholder('Name').fill(name)
  await createDialog.getByRole('button', { name: 'Create' }).click()
  await expect(createDialog).toBeHidden()

  // Locate by a column unique to the headscale-users table (not by index):
  // the "Pending invites" table only renders when invites exist, which
  // would otherwise shift a positional index.
  const hsTable = page.locator('table').filter({ has: page.getByRole('columnheader', { name: 'Linked member' }) })
  await expect(hsTable.getByRole('cell', { name })).toBeVisible()

  // Rename it.
  const renamed = `${name}-renamed`
  const hsRow = hsTable.locator('tr', { hasText: name })
  await hsRow.getByRole('button', { name: 'Rename' }).click()
  const renameDialog = page.locator('dialog', { hasText: `Rename ${name}` })
  await renameDialog.locator('input[type="text"]').fill(renamed)
  await renameDialog.getByRole('button', { name: 'Rename' }).click()
  await expect(hsTable.getByRole('cell', { name: renamed })).toBeVisible()

  // Delete it (confirms the native window.confirm()).
  page.once('dialog', (d) => void d.accept())
  await hsTable.locator('tr', { hasText: renamed }).getByRole('button', { name: 'Delete' }).click()
  await expect(hsTable.getByRole('cell', { name: renamed })).toBeHidden()
})
