import { expect, test } from '@playwright/test'

// Uses a unique tag per run so repeat local runs against the same dev-up
// stack (mingling with existing data is fine) don't collide with keys left
// over from a previous run.
function uniqueTag(): string {
  return `tag:e2e-${Date.now()}`
}

test('creating and expiring a tag-only pre-auth key', async ({ page }) => {
  const tag = uniqueTag()
  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Pre-Auth Keys' })).toBeVisible()

  await page.getByRole('button', { name: 'Create pre-auth key' }).click()
  const dialog = page.locator('dialog.modal[open]')
  await dialog.getByText('Tag only (no owning user)').click()
  await dialog.getByPlaceholder('tag:server, tag:prod').fill(tag)
  await dialog.getByRole('button', { name: 'Create' }).click()

  // The dialog swaps to a "key created" view showing the one-time key.
  await expect(dialog.getByText('Pre-auth key created')).toBeVisible()
  await dialog.getByRole('button', { name: 'Done' }).click()

  const row = page.getByRole('row').filter({ hasText: tag })
  await expect(row).toBeVisible()
  await expect(row.getByText('(Tag Only)')).toBeVisible()

  page.once('dialog', (d) => void d.accept())
  await row.getByRole('button', { name: 'Expire Key' }).click()
  await expect(row.getByText('Expired')).toBeVisible()
})

test('creating and expiring a reusable, ephemeral pre-auth key for a real user', async ({
  page,
}) => {
  const tag = uniqueTag()
  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Pre-Auth Keys' })).toBeVisible()

  await page.getByRole('button', { name: 'Create pre-auth key' }).click()
  const dialog = page.locator('dialog.modal[open]')
  // "Tag only" is unchecked by default, so a real user must be selected.
  const userSelect = dialog.getByRole('combobox')
  await expect(userSelect.locator('option').nth(1)).toBeAttached()
  const userOptions = await userSelect.locator('option').all()
  const firstRealUser = await userOptions[1].getAttribute('value')
  await userSelect.selectOption(firstRealUser ?? '')
  await dialog.getByPlaceholder('tag:server, tag:prod').fill(tag)
  await dialog.locator('label', { hasText: 'Reusable' }).click()
  await dialog.locator('label', { hasText: 'Ephemeral' }).click()
  await dialog.getByRole('button', { name: 'Create' }).click()

  await expect(dialog.getByText('Pre-auth key created')).toBeVisible()
  await dialog.getByRole('button', { name: 'Done' }).click()

  const row = page.getByRole('row').filter({ hasText: tag })
  await expect(row).toBeVisible()
  await expect(row.getByRole('cell', { name: 'Yes' }).first()).toBeVisible()

  page.once('dialog', (d) => void d.accept())
  await row.getByRole('button', { name: 'Expire Key' }).click()
  await expect(row.getByText('Expired')).toBeVisible()
})
