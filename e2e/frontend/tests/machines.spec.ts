import { expect, test } from './support/account'
import { enrollmentTimeoutMs } from './connectivity/environment'
import { confirmDialog } from './support/confirm'
import { startPendingLogin } from './support/pendingLogin'
import { ownMemberID } from './support/realMachine'

test('the client device dialog shows the org-specific login-server command', async ({ page }) => {
  await page.goto('/devices')
  await expect(page.getByRole('heading', { name: 'Devices' })).toBeVisible()

  await page.getByRole('button', { name: 'Add device' }).click()
  await page.getByRole('menuitem', { name: /Client device/ }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('tab', { name: 'Linux' }).click()
  await expect(dialog.locator('code')).toContainText('tailscale up --login-server=https://')

  await dialog.getByRole('tab', { name: 'iOS' }).click()
  await expect(dialog.locator('code')).toHaveText(/^https:\/\//)

  await dialog.getByRole('button', { name: 'Done' }).click()
  await expect(dialog).toBeHidden()
})

test('the server entry opens the pre-auth key dialog', async ({ page }) => {
  await page.goto('/devices')
  await page.getByRole('button', { name: 'Add device' }).click()
  await page.getByRole('menuitem', { name: /Server/ }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'Create pre-auth key' })).toBeVisible()

  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).toBeHidden()
})

test('a waiting device is registered from its pasted login URL', async ({ page, account }) => {
  test.setTimeout(120_000)
  const login = await startPendingLogin(account.org)
  try {
    await page.goto('/devices')
    await page.getByRole('button', { name: 'Add device' }).click()
    await page.getByRole('menuitem', { name: /Waiting device/ }).click()
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('textbox').fill(await login.authURL())
    await dialog.getByRole('combobox').selectOption(await ownMemberID(page))
    await dialog.getByRole('button', { name: 'Register' }).click()
    await expect(dialog).toBeHidden()

    const row = page.getByRole('row').filter({ hasText: login.hostname })
    await expect(row).toBeVisible({ timeout: enrollmentTimeoutMs })

    await row.getByRole('button', { name: 'Actions', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Remove', exact: true }).click()
    await confirmDialog(page, 'Remove')
    await expect(row).toBeHidden()
  } finally {
    await login.close()
  }
})

test('filtering devices by status updates the query string', async ({ page }) => {
  await page.goto('/devices')
  await page.getByRole('combobox').nth(1).selectOption('online')
  await expect(page).toHaveURL(/status=online/)

  await page.getByRole('button', { name: 'Clear' }).click()
  await expect(page).toHaveURL(/\/devices$/)
})
