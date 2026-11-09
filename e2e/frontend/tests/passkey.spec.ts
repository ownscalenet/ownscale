import { randomBytes } from 'node:crypto'
import { expect, test } from './support/account'
import { confirmDialog } from './support/confirm'
import { addVirtualAuthenticator } from './support/webauthn'

const PASSKEY_IDENTITY_NAME = 'passkey'

// Adds a passkey to the worker's account from User settings, then uses it
// to sign back in.
test('adding a passkey in User settings and signing in with it', async ({ page }) => {
  // Unique per run: an interrupted run leaves its passkey behind on a
  // persistent (remote) stack, and a shared name would then match several rows.
  const passkeyName = `E2E test passkey ${randomBytes(3).toString('hex')}`
  await addVirtualAuthenticator(page)

  await page.goto('/user-settings')
  await expect(page.getByRole('heading', { name: 'Passkeys' })).toBeVisible()

  await page.getByRole('button', { name: 'Add a passkey' }).click()
  const nameDialog = page.getByRole('dialog')
  await nameDialog.getByRole('textbox').fill(passkeyName)
  await nameDialog.getByRole('button', { name: 'Add passkey', exact: true }).click()

  const row = page.getByRole('row').filter({ hasText: passkeyName })
  await expect(row).toBeVisible({ timeout: 15_000 })

  await page.getByRole('button', { name: 'Logout' }).click()
  await expect(page).toHaveURL(/\/login/)

  await page.getByRole('button', { name: 'Sign in with a passkey' }).click()
  await expect(page.getByRole('button', { name: 'Logout' })).toBeVisible({ timeout: 15_000 })

  // Clean up: remove the passkey this test added so repeat runs start fresh.
  await page.goto('/user-settings')
  await row.getByRole('button', { name: 'Delete' }).click()
  await confirmDialog(page, 'Delete passkey')
  await expect(row).toBeHidden()
})

// Self-registers a throwaway passkey user, signs back in with the same
// passkey, then deletes the account and its org so runs leave nothing behind.
test.describe('signing up with a passkey', () => {
  // test.use at file scope would also log out the test above.
  test.use({ storageState: { cookies: [], origins: [] } })

  test('a new passkey user can sign back in and delete their account', async ({ page }) => {
    test.setTimeout(120_000)
    const username = `e2e-${randomBytes(4).toString('hex')}`
    await addVirtualAuthenticator(page)

    await page.goto('/login')
    await page.getByRole('link', { name: 'Create an account' }).click()
    await expect(page).toHaveURL(/\/signup/)
    await page.locator('#passkey-username').fill(username)
    await page.getByRole('button', { name: 'Continue with passkey' }).click()
    const logout = page.getByRole('button', { name: 'Logout' })
    await expect(logout).toBeVisible({ timeout: 60_000 })
    try {
      await logout.click()
      await expect(page).toHaveURL(/\/login/)
      await page.getByRole('button', { name: 'Sign in with a passkey' }).click()
      await expect(logout).toBeVisible({ timeout: 15_000 })
      const session = await page.request.get('/api/session').then(r => r.json())
      expect(session.user?.Name).toBe(username)
    } finally {
      const deleted = await page.request.delete('/api/account')
      expect(deleted.ok(), `DELETE /api/account: HTTP ${deleted.status()}`).toBeTruthy()
    }
    expect((await page.request.get('/api/session')).ok()).toBeFalsy()
  })
})
