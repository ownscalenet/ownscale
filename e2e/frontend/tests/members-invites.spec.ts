import { expect, test } from '@playwright/test'

test('creating and revoking an invite link', async ({ page }) => {
  await page.goto('/users')
  await expect(page.getByRole('heading', { name: 'Users' })).toBeVisible()

  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.locator('dialog.modal[open]')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()

  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const link = dialog.getByRole('textbox')
  await expect(link).toHaveValue(/\/invite\//)
  await dialog.getByRole('button', { name: 'Done' }).click()

  await expect(page.getByRole('heading', { name: 'Pending invites' })).toBeVisible()
  const row = page.getByRole('row').filter({ hasText: '(link only)' }).first()
  await expect(row).toBeVisible()

  await row.getByRole('button', { name: 'Revoke' }).click()
  await expect(row).toBeHidden()
})
