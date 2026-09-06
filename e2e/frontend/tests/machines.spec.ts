import { expect, test } from '@playwright/test'

test('the register-machine dialog shows the org-specific login-server command', async ({
  page,
}) => {
  await page.goto('/machines')
  await expect(page.getByRole('heading', { name: 'Machines' })).toBeVisible()

  await page.getByRole('button', { name: 'Register machine' }).click()
  const dialog = page.locator('dialog.modal[open]')
  await expect(dialog.locator('code')).toContainText('tailscale up --login-server=https://')

  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).toBeHidden()
})

test('filtering machines by status updates the query string', async ({ page }) => {
  await page.goto('/machines')
  await page.getByRole('combobox').nth(1).selectOption('online')
  await expect(page).toHaveURL(/status=online/)

  await page.getByRole('button', { name: 'Clear' }).click()
  await expect(page).toHaveURL(/\/machines$/)
})
