import { readFileSync } from 'node:fs'
import { expect, test } from './support/account'
import { confirmDialog } from './support/confirm'
import { addVirtualAuthenticator, importCredential } from './support/webauthn'

const PASSKEY_IDENTITY_NAME = 'passkey'
const PASSKEY_CREDENTIAL_STATE = 'tests/.auth/passkey-credential.json'

// This test adds a passkey to the worker's already-provisioned account from
// User settings, then uses it to sign back in. It deliberately reuses the
// worker's authenticated storageState (unlike login.spec.ts)
// rather than exercising brand-new self-registration, since that path
// consumes one of the dev stack's fixed seed organizations -- brand-new
// passkey self-registration is instead exercised once, deterministically,
// in global.setup.ts's bootstrap (see the test below, which signs back in
// with that bootstrapped identity's passkey instead of registering
// another). The invite-skip-provisioning case is covered at the Go handler
// level in handlers/passkey_test.go.
test('adding a passkey in User settings and signing in with it', async ({ page }) => {
  await addVirtualAuthenticator(page)

  await page.goto('/user-settings')
  await expect(page.getByRole('heading', { name: 'Passkeys' })).toBeVisible()

  await page.getByRole('button', { name: 'Add a passkey' }).click()
  const nameDialog = page.getByRole('dialog')
  await nameDialog.getByRole('textbox').fill('E2E test passkey')
  await nameDialog.getByRole('button', { name: 'Add passkey', exact: true }).click()

  const row = page.getByRole('row').filter({ hasText: 'E2E test passkey' })
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

// Unauthenticated sign-in as the fixed passkey identity that
// global.setup.ts's bootstrap self-registered into org "dddd" (see
// PASSKEY_IDENTITY there). Rather than self-registering another brand-new
// user here (which would use up one of the fixed subdomains handed out by
// orgs.Service.NextSubdomain), this replays the credential that bootstrap saved -- exercising
// the discoverable/usernameless passkey login as "logging in again in
// some other test" with the *same* passkey, consuming no additional org.
//
// Wrapped in its own describe block: test.use() applies to the whole file's
// top-level suite when called at file scope, so without this it would also
// clear the authenticated storageState for the "adding a passkey in
// Settings" test above.
test.describe('signing back in with the bootstrapped passkey', () => {
  test.use({ storageState: { cookies: [], origins: [] } })

  test('the bootstrapped passkey user can sign back in with the same passkey', async ({ page }) => {
    const auth = await addVirtualAuthenticator(page)
    const saved = JSON.parse(readFileSync(PASSKEY_CREDENTIAL_STATE, 'utf-8'))
    await importCredential(auth, saved.credential)

    await page.goto('/login')
    await page.getByRole('button', { name: 'Sign in with a passkey' }).click()
    try {
      await expect(page.getByRole('button', { name: 'Logout' })).toBeVisible({ timeout: 15_000 })
    } catch (err) {
      // This test relies on global.setup.ts's bootstrap having already
      // registered PASSKEY_IDENTITY and saved a *currently valid* credential.
      throw new Error(
        `sign-in with the saved passkey credential failed; re-run global.setup.ts's bootstrap ` +
        `(or delete ${PASSKEY_CREDENTIAL_STATE} to force it to re-register) if the backend's ` +
        `database was reset since that credential was saved: ${err}`,
      )
    }

    const session = await page.request.get('/api/session').then(r => r.json())
    expect(session.user?.Name).toBe(PASSKEY_IDENTITY_NAME)
  })
})
