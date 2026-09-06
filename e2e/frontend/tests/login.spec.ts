import { expect, test } from '@playwright/test'

// This spec drives the real SSO login/logout flow itself, so it can't
// reuse the "chromium" project's cached storageState (see
// playwright.config.ts) -- it needs to start every test unauthenticated.
test.use({ storageState: { cookies: [], origins: [] } })

test('signing in via SSO reaches the app, and logout returns to /login', async ({ page }) => {
  // Provisioning a brand-new organization can take longer than Playwright's
  // default 30s test timeout on a completely fresh dev-up stack.
  test.setTimeout(90_000)

  await page.goto('/login')
  await expect(page.getByRole('heading', { name: 'Ownscale console' })).toBeVisible()

  await page.getByRole('link', { name: 'Sign in with SSO' }).click()
  await page.locator('#login').fill('admin@ownscale.test')
  await page.locator('#password').fill('ownscale')
  await page.locator('#submit-login').click()

  // Provisioning a brand-new organization can take a few seconds (see
  // CreateOrganization.tsx); either way this ends with the app's sidebar.
  const logout = page.getByRole('button', { name: 'Logout' })
  await expect(logout).toBeVisible({ timeout: 60_000 })

  await logout.click()
  await expect(page).toHaveURL(/\/login/)
})

test('an unauthenticated visit to a protected page redirects to /login', async ({ page }) => {
  await page.goto('/machines')
  await expect(page).toHaveURL(/\/login\?next=%2Fmachines/)
})
