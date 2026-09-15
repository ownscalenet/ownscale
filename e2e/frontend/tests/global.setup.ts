import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { expect, test as setup, type Page } from '@playwright/test'
import { baseDomain, baseURL } from './connectivity/environment'
import { identities, OrgUI } from './connectivity/ui'
import { addVirtualAuthenticator, clearCredentials, exportCredential, importCredential } from './support/webauthn'

const STORAGE_STATE = 'tests/.auth/admin.json'

// Fourth fixed identity, bootstrapped the same way as admin/user1/user2
// below but via passkey self-registration instead of Dex SSO, so it lands
// on the next seed org in the fixed pool (see orgs.DefaultSeedSubdomains)
// deterministically rather than racing other specs for it.
const PASSKEY_IDENTITY = { email: 'passkey@ownscale.test', org: 'dddd' }

// Persists the WebAuthn credential minted the first time PASSKEY_IDENTITY
// self-registers, so later runs (and passkey.spec.ts's "sign in again"
// test) can replay the *same* passkey via importCredential instead of
// registering another brand-new user/org. Gitignored alongside
// STORAGE_STATE (see e2e/frontend's tests/.auth/ entry in .gitignore).
const PASSKEY_CREDENTIAL_STATE = 'tests/.auth/passkey-credential.json'

// Drives the "New here? Create an account with a passkey" self-registration
// flow for PASSKEY_IDENTITY, starting from a fresh /login page.
async function registerFreshPasskey(page: Page) {
  await page.getByRole('button', { name: 'New here? Create an account with a passkey' }).click()
  await page.locator('#passkey-email').fill(PASSKEY_IDENTITY.email)
  await page.getByRole('button', { name: 'Continue with passkey' }).click()
}

// Bootstraps the dev stack's three fixed Dex identities -- admin, user1,
// user2 -- into their required organizations -- aaaa, bbbb, cccc,
// respectively (see tests/connectivity/ui.ts's `identities`) -- plus the
// fixed passkey identity below into dddd, in that exact order, before any
// other spec in this suite runs.
//
// orgs.Provisioner hands out its fixed seed orgs strictly FIFO to whoever
// self-registers first; it has no notion of "reserved for this identity".
// admin/user1/user2 need to be the first three to register so they land on
// aaaa/bbbb/cccc as tests/connectivity relies on (see e2e/README.md), and
// the passkey identity needs to be the fourth so it reliably lands on
// dddd. Any other self-registering spec (onboarding.spec.ts) that runs
// before this bootstrap would race ahead and steal one of those seed
// orgs -- this happened in practice once passkey self-registration was
// exercised as its own later spec with a brand-new random user each run,
// which is why that logic now lives here instead. Running this bootstrap
// first, unconditionally, in the "setup" project (which "chromium" already
// depends on -- see playwright.config.ts) removes that race entirely,
// regardless of which order/how many times e2e-frontend and
// e2e-connectivity are invoked: OrgUI.login is a real SSO login + org
// readiness/assignment check, so it's a harmless idempotent re-login (not
// a fresh registration) whenever an identity already exists; likewise the
// passkey step below replays a saved credential instead of re-registering
// once PASSKEY_IDENTITY already exists.
//
// describe.serial forces these to run in declared order, in a single
// worker, regardless of this config's fullyParallel/workers settings --
// without it, Playwright could run them concurrently across workers and
// reintroduce the same race.
setup.describe.serial('bootstrap fixed dev identities', () => {
  for (const identity of identities) {
    setup(`${identity.name} claims ${identity.org}`, async ({ browser }) => {

      const ui = await OrgUI.login(browser, identity)
      try {
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

  // Claims the fourth seed org (dddd) via passkey self-registration,
  // right after admin/user1/user2 claim aaaa/bbbb/cccc above. On a fresh stack this
  // registers PASSKEY_IDENTITY for the first time and saves the resulting
  // credential; on a repeat run it replays the saved credential to sign
  // back in, which is idempotent.
  setup(`passkey user claims ${PASSKEY_IDENTITY.org}`, async ({ browser }) => {
    setup.setTimeout(90_000)

    const context = await browser.newContext({ baseURL, ignoreHTTPSErrors: true })
    const page = await context.newPage()
    try {
      const auth = await addVirtualAuthenticator(page)
      // Tracks whether *this run* freshly registered the passkey identity
      // (as opposed to successfully signing back in with a previously
      // saved credential), so we know whether to (over)write
      // PASSKEY_CREDENTIAL_STATE below.
      let registeredFresh = false

      await page.goto('/login')
      if (existsSync(PASSKEY_CREDENTIAL_STATE)) {
        const saved = JSON.parse(readFileSync(PASSKEY_CREDENTIAL_STATE, 'utf-8'))
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
      if (session.user?.Email !== PASSKEY_IDENTITY.email) {
        throw new Error(`${PASSKEY_IDENTITY.org}: unexpected signed-in passkey user`)
      }
      if (session.organization?.Domain !== `${PASSKEY_IDENTITY.org}.${baseDomain}`) {
        throw new Error(`expected passkey belongs to: ${PASSKEY_IDENTITY.org}.${baseDomain}; got: ${session.organization?.Domain}`)
      }

      if (registeredFresh) {
        const credential = await exportCredential(auth, baseDomain)
        mkdirSync(dirname(PASSKEY_CREDENTIAL_STATE), { recursive: true })
        writeFileSync(PASSKEY_CREDENTIAL_STATE, JSON.stringify({ email: PASSKEY_IDENTITY.email, credential }))
      }
    } finally {
      await context.close()
    }
  })
})

