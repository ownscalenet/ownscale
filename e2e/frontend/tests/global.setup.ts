import { lookup } from 'node:dns/promises'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { expect, test as setup, type Page } from '@playwright/test'
import { baseDomain, baseURL, consoleDomain } from './connectivity/environment'
import { identities, OrgUI } from './connectivity/ui'
import { accountNames, accountsFile, emailOf, passkeyCredentialFile, removeInvitee, resetAuthRestrictions, signIn, storageStateOf, workerAccounts } from './support/account'
import { addVirtualAuthenticator, clearCredentials, exportCredential, importCredential } from './support/webauthn'

const STORAGE_STATE = 'tests/.auth/admin.json'

// Fourth fixed identity, bootstrapped the same way as admin/user1/user2
// below but via passkey self-registration instead of Dex SSO, so it lands
// on the next fixed subdomain (see orgs.Service.NextSubdomain)
// deterministically rather than racing other specs for it.
const PASSKEY_IDENTITY = 'passkey'

// Drives the "Create an account" passkey self-registration flow for
// PASSKEY_IDENTITY, starting from a fresh /login page.
async function registerFreshPasskey(page: Page) {
  await page.getByRole('link', { name: 'Create an account' }).click()
  await expect(page).toHaveURL(/\/signup/)
  await page.locator('#passkey-username').fill(PASSKEY_IDENTITY)
  await page.getByRole('button', { name: 'Continue with passkey' }).click()
}

// Bootstraps the dev stack's three fixed Dex identities: admin, user1, user2
// and their organizations: aaaa, bbbb, cccc.
//
// orgs.Service.NextSubdomain hands out the fixed subdomains in order to
// whoever self-registers first; it has no notion of "reserved for this
// identity".
// admin/user1/user2 need to be the first three to register so they land on
// aaaa/bbbb/cccc.
setup.describe.serial('bootstrap fixed dev identities', () => {
  for (const identity of identities) {
    setup(`${identity.name} claims ${identity.org}`, async ({ browser }) => {

      const ui = await OrgUI.login(browser, identity)
      try {
        // Undoes any restrictive allowlist a previous interrupted run left
        // in place (see settings-apply.spec.ts's "login restrictions are
        // enforced by headscale"), so every run starts from a known-good
        // state regardless of the stack's history.
        await resetAuthRestrictions(ui.page)

        // Every other spec depends on this cached admin session (see
        // playwright.config.ts's "chromium" project); user1/user2 only
        // need their organization claimed, not a saved session.
        if (identity.name === 'admin') {
          await ui.context.storageState({ path: STORAGE_STATE })
        }
      } finally {
        await ui.context.close()
      }
    })
  }

  // Creates the fourth fixed org (dddd) via passkey self-registration,
  // right after admin/user1/user2 claim aaaa/bbbb/cccc above. On a fresh stack this
  // registers PASSKEY_IDENTITY for the first time and saves the resulting
  // credential; on a repeat run it replays the saved credential to sign
  // back in, which is idempotent.
  setup(`passkey user claims an org}`, async ({ browser }) => {
    setup.setTimeout(90_000)

    const context = await browser.newContext({ baseURL, ignoreHTTPSErrors: true })
    const page = await context.newPage()
    try {
      const auth = await addVirtualAuthenticator(page)
      // Tracks whether *this run* freshly registered the passkey identity
      // (as opposed to successfully signing back in with a previously
      // saved credential), so we know whether to (over)write
      // passkeyCredentialFile below.
      let registeredFresh = false

      await page.goto('/login')
      if (existsSync(passkeyCredentialFile)) {
        const saved = JSON.parse(readFileSync(passkeyCredentialFile, 'utf-8'))
        await importCredential(auth, saved.credential)
        await page.getByRole('button', { name: 'Sign in with a passkey' }).click()

        // The saved credential went stale because database reset.
        const outcome = await Promise.race([
          page.getByRole('button', { name: 'Logout' }).waitFor({ state: 'visible', timeout: 20_000 }).then(() => 'success' as const),
          page.locator('.alert-error').waitFor({ state: 'visible', timeout: 20_000 }).then(() => 'failure' as const),
        ]).catch(() => 'failure' as const)

        if (outcome === 'failure') {
          // Drop the stale credential from the authenticator first --
          // otherwise it and the freshly registered one would both match
          // rpId below and exportCredential's "exactly one match" check
          // would fail.
          await clearCredentials(auth)
          await page.goto('/login')
          await registerFreshPasskey(page)
          registeredFresh = true
        }
      } else {
        await registerFreshPasskey(page)
        registeredFresh = true
      }
      await expect(page.getByRole('button', { name: 'Logout' })).toBeVisible({ timeout: 60_000 })

      const session = await page.request.get('/api/session').then(r => r.json())
      if (session.user?.Name !== PASSKEY_IDENTITY) {
        throw new Error(`expected: ${PASSKEY_IDENTITY}; got: ${session.user?.Name}`)
      }

      if (registeredFresh) {
        const credential = await exportCredential(auth, consoleDomain)
        mkdirSync(dirname(passkeyCredentialFile), { recursive: true })
        writeFileSync(passkeyCredentialFile, JSON.stringify({ username: PASSKEY_IDENTITY, org: session.organization?.Domain, credential }))
      }

      await resetAuthRestrictions(page)
    } finally {
      await context.close()
    }
  })

  // The e2e accounts (see support/account.ts) sign in after the identities
  // above, so on a fresh stack they get the next fixed subdomains.
  for (const name of accountNames) {
    setup(`${name} claims an org`, async ({ browser }) => {
      const context = await browser.newContext({ baseURL, ignoreHTTPSErrors: true })
      try {
        const page = await context.newPage()
        await signIn(page, emailOf(name))
        const session = await page.request.get('/api/session').then(r => r.json())
        const org = String(session.organization?.Domain ?? '').split('.')[0]
        if (!org) throw new Error(`${name} has no organization`)
        await context.storageState({ path: storageStateOf(name) })

        const orgs = existsSync(accountsFile) ? JSON.parse(readFileSync(accountsFile, 'utf-8')) : {}
        orgs[name] = org
        writeFileSync(accountsFile, JSON.stringify(orgs, null, 2))
        await checkHost(org)
        await resetAuthRestrictions(page)
        const invitee = workerAccounts.find(a => a.name === name)?.invitee
        if (invitee) await removeInvitee(page, invitee)
      } finally {
        await context.close()
      }
    })
  }
})

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
