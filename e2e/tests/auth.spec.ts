import { test, expect } from '@playwright/test'
import { ADMIN_EMAIL, loginViaDex } from '../lib/dex'

// This deliberately re-runs a full, real Dex login from a fresh (no
// storageState) context -- global.setup.ts also logs in, but only as a
// means to provision the shared e2e organization. This spec exists to
// assert the login UX itself (redirect to Dex, credential submission,
// redirect back, authenticated session) works end-to-end on its own.
test.use({ storageState: { cookies: [], origins: [] } })

test('signs in via the upstream OIDC provider (Dex)', async ({ page }) => {
  await page.goto('/login')
  await expect(page.getByRole('link', { name: 'Sign in with SSO' })).toBeVisible()

  await loginViaDex(page, ADMIN_EMAIL)

  // A signed-in admin already belongs to the (already-provisioned, by
  // global.setup.ts) e2e organization, so they land in the app itself
  // rather than on /create-organization.
  await expect(page).not.toHaveURL(/\/login/)
  await expect(async () => {
    const session = await page.request.get('/api/session')
    expect(session.ok()).toBeTruthy()
  }).toPass()
  const session = await page.request.get('/api/session')
  const body = await session.json()
  expect(body.user.Email).toBe(ADMIN_EMAIL)
})
