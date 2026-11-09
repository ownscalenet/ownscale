import { lookup } from 'node:dns/promises'
import { test as setup } from '@playwright/test'
import { baseDomain, baseURL, localTLS } from './connectivity/environment'
import { accounts, emailOf, removeInvitee, resetAuthRestrictions, signIn, storageStateOf } from './support/account'

// Each config's setup project picks the accounts it needs (setupProject).
setup.describe.configure({ mode: 'parallel' })

for (const account of accounts) {
  setup(`${account.name} signs in`, async ({ browser }) => {
    const context = await browser.newContext({ baseURL, ignoreHTTPSErrors: localTLS })
    try {
      const page = await context.newPage()
      await signIn(page, emailOf(account.name))
      const session = await page.request.get('/api/session').then(r => r.json())
      const org = String(session.organization?.Domain ?? '').split('.')[0]
      if (org !== account.name) {
        throw new Error(`${account.name} landed on org "${org}", not "${account.name}": ` +
          `the stack needs ${account.name} in OWNSCALE_FIXED_SUBDOMAINS, with no other org on that subdomain`)
      }
      await context.storageState({ path: storageStateOf(account.name) })
      await checkHost(org)
      // A previous interrupted run may have left a restrictive allowlist or invitee behind.
      await resetAuthRestrictions(page)
      if ('invitee' in account) await removeInvitee(page, account.invitee)
    } finally {
      await context.close()
    }
  })
}

// checkHost fails with the missing /etc/hosts line when org's hostname
// doesn't resolve on a local stack: real machines reach the org's headscale
// by name.
async function checkHost(org: string) {
  if (baseDomain !== 'ownscale.test') return
  const name = `${org}.${baseDomain}`
  await lookup(name).catch(() => {
    throw new Error(`${name} doesn't resolve; add to /etc/hosts:\n127.0.0.1 ${name}`)
  })
}
