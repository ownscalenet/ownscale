import { test as setup } from '@playwright/test'

// Seeded by etc/dex/config.yaml's staticPasswords -- see README.md for how
// to add more test users if a spec needs a second identity.
const ADMIN_EMAIL = 'admin@ownscale.test'
const ADMIN_PASSWORD = 'ownscale'

const STORAGE_STATE = 'tests/.auth/admin.json'

// Runs once before the "chromium" project (see playwright.config.ts's
// project dependency): logs in for real through the full SSO redirect
// chain (Ownscale /login -> Dex -> callback) and saves the resulting
// session cookie, so every other spec starts already authenticated
// instead of repeating this flow itself.
setup('authenticate as admin', async ({ page }) => {
  // Provisioning a brand-new organization can take longer than Playwright's
  // default 30s test timeout on a completely fresh dev-up stack.
  setup.setTimeout(90_000)

  await page.goto('/login')
  await page.getByRole('link', { name: 'Sign in with SSO' }).click()

  // Lands on Dex's own login form (see etc/dex/config.yaml, "dex" service
  // in docker-compose.yml) -- a plain server-rendered form, not part of
  // the SPA.
  await page.locator('#login').fill(ADMIN_EMAIL)
  await page.locator('#password').fill(ADMIN_PASSWORD)
  await page.locator('#submit-login').click()

  // Back on the SPA, authenticated. A brand-new user is bounced through
  // /create-organization first (provisioning, then an automatic redirect
  // once active -- see CreateOrganization.tsx), so this generously waits
  // out that whole chain rather than assuming the admin already has an
  // organization from a prior local dev-up run.
  await page.getByRole('button', { name: 'Logout' }).waitFor({ timeout: 60_000 })

  await page.context().storageState({ path: STORAGE_STATE })
})
