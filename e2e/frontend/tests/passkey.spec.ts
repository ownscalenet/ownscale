import { expect, test } from '@playwright/test'

// addVirtualAuthenticator wires up Chrome DevTools Protocol's WebAuthn
// domain so the browser answers navigator.credentials.create()/.get()
// itself, without a real security key or platform authenticator. The
// authenticator is configured as a resident-key-capable, pre-verified
// "platform" authenticator so both the register (createPasskey) and the
// discoverable/usernameless login (getPasskey) ceremonies succeed without
// any UI prompt.
async function addVirtualAuthenticator(page: import('@playwright/test').Page) {
  const client = await page.context().newCDPSession(page)
  await client.send('WebAuthn.enable')
  await client.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  })
}

// This test adds a passkey to the already-provisioned admin account from
// Settings, then uses it to sign back in. It deliberately reuses the
// "chromium" project's authenticated storageState (unlike login.spec.ts)
// rather than exercising brand-new self-registration, since that path (see
// the test below) consumes one of the dev stack's fixed seed organizations.
// The invite-skip-provisioning case is covered at the Go handler level in
// handlers/passkey_test.go.
test('adding a passkey in Settings and signing in with it', async ({ page }) => {
  await addVirtualAuthenticator(page)

  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Passkeys' })).toBeVisible()

  page.once('dialog', (dialog) => void dialog.accept('E2E test passkey'))
  await page.getByRole('button', { name: 'Add a passkey' }).click()

  const row = page.getByRole('row').filter({ hasText: 'E2E test passkey' })
  await expect(row).toBeVisible({ timeout: 15_000 })

  await page.getByRole('button', { name: 'Logout' }).click()
  await expect(page).toHaveURL(/\/login/)

  await page.getByRole('button', { name: 'Sign in with a passkey' }).click()
  await expect(page.getByRole('button', { name: 'Logout' })).toBeVisible({ timeout: 15_000 })

  // Clean up: remove the passkey this test added so repeat runs start fresh.
  await page.goto('/settings')
  page.once('dialog', (dialog) => void dialog.accept())
  await row.getByRole('button', { name: 'Delete' }).click()
  await expect(row).toBeHidden()
})

// Unauthenticated, brand-new-user self-registration via passkey. This
// consumes one organization from the dev stack's fixed seed pool
// (orgs.DefaultSeedSubdomains, now 6 wide specifically so this test can
// coexist with the rest of the e2e suite without exhausting it -- see
// e2e/README.md and orgs/provisioner.go).
//
// Wrapped in its own describe block: test.use() applies to the whole file's
// top-level suite when called at file scope, so without this it would also
// clear the authenticated storageState for the "adding a passkey in
// Settings" test above.
test.describe('new-user registration', () => {
  test.use({ storageState: { cookies: [], origins: [] } })

  test('a brand-new user can self-register with a passkey and gets a fresh org', async ({
    page,
  }) => {
    // Provisioning a brand-new organization can take longer than
    // Playwright's default 30s test timeout, so this is bumped explicitly
    // (mirrors onboarding.spec.ts's equivalent OAuth self-registration test).
    test.setTimeout(90_000)

    await addVirtualAuthenticator(page)

    await page.goto('/login')
    await page.getByRole('button', { name: 'New here? Create an account with a passkey' }).click()
    await page.locator('#passkey-email').fill(`e2e-passkey-${Date.now()}@ownscale.test`)
    await page.getByRole('button', { name: 'Continue with passkey' }).click()

    await expect(page.getByRole('button', { name: 'Logout' })).toBeVisible({ timeout: 60_000 })
  })
})
