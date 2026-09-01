import { test as setup, expect } from '@playwright/test'
import { resetAndStartStack } from '../lib/stack'
import { ADMIN_EMAIL, loginViaDex } from '../lib/dex'

// The subdomain used for the e2e org. Already has an /etc/hosts entry on
// this machine (127.0.0.1 acme.ownscale.test), so no host-file edits are
// needed to run this suite.
const ORG_SUBDOMAIN = 'acme'

// Real end-to-end setup, run once before every other spec file (see
// playwright.config.ts's "setup" project + "chromium" project's
// `dependencies: ['setup']`): resets the local stack/database, logs in as
// the admin user through the real Dex OIDC flow, creates and waits for
// the e2e organization to finish provisioning (a real headscale
// container), then saves the authenticated session for reuse.
setup('reset stack and provision e2e organization', async ({ page, baseURL }) => {
  // resetAndStartStack alone can take well over a minute (docker compose
  // stop/up, migrations), and org provisioning below spins up a real
  // headscale container -- give this generous headroom well beyond the
  // default per-test timeout.
  setup.setTimeout(300_000)

  await resetAndStartStack()

  await loginViaDex(page, ADMIN_EMAIL)

  // loginViaDex leaves us wherever the app's own redirect chain lands --
  // a brand-new admin with no organization yet ends up on
  // /create-organization.
  await page.waitForURL(/\/create-organization/)
  await page.getByLabel(/subdomain/i).fill(ORG_SUBDOMAIN)
  await page.getByRole('button', { name: /create/i }).click()

  // Organization provisioning spins up a real Docker container (headscale);
  // poll the session API directly rather than the UI, both to avoid racing
  // CreateOrganization.tsx's own auto-redirect effect and because it's a
  // more direct/reliable readiness signal.
  await expect
    .poll(
      async () => {
        const res = await page.request.get('/api/session')
        const body = await res.json()
        return body.organization?.Status
      },
      { timeout: 120_000, intervals: [1000] },
    )
    .toBe('active')

  // CreateOrganization.tsx auto-navigates the browser to the ownscale
  // console's own home page the instant it observes the org go active;
  // explicitly navigate there too so the saved storage state reflects
  // being logged into the console, on a console page.
  await page.goto(`${baseURL}/`)
  await expect(page).toHaveURL(/\/(machines|users|access-control)?$/)

  await page.context().storageState({ path: '.auth/admin.json' })
})
