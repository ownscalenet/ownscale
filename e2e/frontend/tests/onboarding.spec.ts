import { emailOf, expect, test } from './support/account'

// Signs in as a connectivity account, which no frontend worker uses.
test.use({ storageState: { cookies: [], origins: [] } })

test('a new user is provisioned an organization and lands on /machines', async ({ page }) => {
  // Provisioning a brand-new organization can take longer than Playwright's
  // default 30s test timeout, so this is bumped explicitly rather than
  // relying only on the expect() timeout below.
  test.setTimeout(90_000)

  await page.goto('/login')
  await page.getByRole('link', { name: 'Sign in with SSO' }).click()
  await page.locator('#login').fill(emailOf('bbbb'))
  await page.locator('#password').fill('ownscale')
  await page.locator('#submit-login').click()

  await expect(page.getByRole('link', { name: 'Machines' })).toBeVisible({ timeout: 5_000 })
  await expect(page).toHaveURL(/\/machines$/)
})
