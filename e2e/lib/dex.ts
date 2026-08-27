import type { Page } from '@playwright/test'

// DEX_PASSWORD is shared by every static user in etc/dex/config.yaml.
export const DEX_PASSWORD = 'ownscale'

export const ADMIN_EMAIL = 'admin@ownscale.test'
export const INVITEE_EMAIL = 'user1@ownscale.test'

export async function loginViaDex(page: Page, email: string, password = DEX_PASSWORD): Promise<void> {
  // Preserve the current /login URL (with any query string, e.g.
  // AcceptInvite.tsx's redirect to /login?next=/invite/:token)
  const current = new URL(page.url())
  const loginUrl = current.pathname === '/login' ? page.url() : '/login'

  // requests that reconfigure network settings trigger in-flight requests to
  // be cancelled by the browser. See DECISIONS.md#2
  const maxAttempts = 3
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      await page.goto(loginUrl)
      await page.getByRole('link', { name: 'Sign in with SSO' }).click()
      await page.waitForURL(/dex\./)
      await page.locator('#login').fill(email)
      await page.locator('#password').fill(password)
      await Promise.all([
        page.waitForURL((url) => !url.hostname.startsWith('dex.'), { timeout: 15_000 }),
        page.locator('#submit-login').click(),
      ])
      return
    } catch (err) {
      if (attempt === maxAttempts) throw err
    }
  }
}
