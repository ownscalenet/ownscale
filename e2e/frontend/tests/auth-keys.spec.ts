import { expect, test } from './support/account'
import { ownMemberID } from './support/realMachine'
import { confirmDialog } from './support/confirm'

// Uses a unique tag per run so repeat local runs against the same dev-up
// stack (mingling with existing data is fine) don't collide with keys left
// over from a previous run.
function uniqueTag(): string {
  return `tag:e2e-${Date.now()}`
}

test('creating, expiring and deleting a tag-only pre-auth key', async ({ page }) => {
  const tag = uniqueTag()
  await page.goto('/pre-auth-keys')
  await expect(page.getByRole('heading', { name: 'Pre-auth Keys' })).toBeVisible()

  await page.getByRole('button', { name: 'Create pre-auth key' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByText('Tag only (no owning user)').click()
  await dialog.getByPlaceholder('tag:server, tag:prod').fill(tag)
  await dialog.getByRole('button', { name: 'Create' }).click()

  // The dialog swaps to a "key created" view showing the one-time key.
  await expect(dialog.getByText('Pre-auth key created')).toBeVisible()
  await dialog.getByRole('button', { name: 'Done' }).click()

  const row = page.getByRole('row').filter({ hasText: tag })
  await expect(row).toBeVisible()
  await expect(row.getByText('(Tag Only)')).toBeVisible()

  await row.getByRole('button', { name: 'Expire Key' }).click()
  await confirmDialog(page, 'Expire key')
  await expect(row.getByText('Expired')).toBeVisible()

  await row.getByRole('button', { name: 'Delete' }).click()
  await confirmDialog(page, 'Delete key')
  await expect(row).toBeHidden()
  // The key must be gone from headscale, not just from the page.
  const res = await page.request.get('/api/auth-keys')
  expect(res.ok()).toBe(true)
  expect(await res.text()).not.toContain(tag)
})

test('creating and expiring a reusable, ephemeral pre-auth key for a real user', async ({
  page,
}) => {
  const tag = uniqueTag()
  await page.goto('/pre-auth-keys')
  await expect(page.getByRole('heading', { name: 'Pre-auth Keys' })).toBeVisible()

  await page.getByRole('button', { name: 'Create pre-auth key' }).click()
  const dialog = page.getByRole('dialog')
  // "Tag only" is unchecked by default, so a real user must be selected.
  const userSelect = dialog.getByRole('combobox')
  await userSelect.selectOption(await ownMemberID(page))
  await dialog.getByPlaceholder('tag:server, tag:prod').fill(tag)
  await dialog.locator('label', { hasText: 'Reusable' }).click()
  await dialog.locator('label', { hasText: 'Ephemeral' }).click()
  await dialog.getByRole('button', { name: 'Create' }).click()

  await expect(dialog.getByText('Pre-auth key created')).toBeVisible()
  await dialog.getByRole('button', { name: 'Done' }).click()

  const row = page.getByRole('row').filter({ hasText: tag })
  await expect(row).toBeVisible()
  await expect(row.getByRole('cell', { name: 'Yes' }).first()).toBeVisible()

  await row.getByRole('button', { name: 'Expire Key' }).click()
  await confirmDialog(page, 'Expire key')
  await expect(row.getByText('Expired')).toBeVisible()

  await row.getByRole('button', { name: 'Delete' }).click()
  await confirmDialog(page, 'Delete key')
  await expect(row).toBeHidden()
  const res = await page.request.get('/api/auth-keys')
  expect(res.ok()).toBe(true)
  expect(await res.text()).not.toContain(tag)
})
