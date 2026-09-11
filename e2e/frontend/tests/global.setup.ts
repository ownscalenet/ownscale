import { test as setup } from '@playwright/test'
import { identities, OrgUI } from './connectivity/ui'

const STORAGE_STATE = 'tests/.auth/admin.json'

// Bootstraps the dev stack's three fixed Dex identities -- admin, user1,
// user2 -- into their required organizations -- aaaa, bbbb, cccc,
// respectively (see tests/connectivity/ui.ts's `identities`) -- in that
// exact order, before any other spec in this suite runs.
//
// orgs.Provisioner hands out its fixed seed orgs strictly FIFO to whoever
// self-registers first; it has no notion of "reserved for this identity".
// admin/user1/user2 need to be the first three to register so they land on
// aaaa/bbbb/cccc as tests/connectivity relies on (see e2e/README.md). Any
// other self-registering spec (onboarding.spec.ts, passkey.spec.ts's
// new-user test) that runs before this bootstrap would race ahead and
// steal one of those three orgs -- this happened in practice once the
// passkey self-registration test was added. Running this bootstrap first,
// unconditionally, in the "setup" project (which "chromium" already
// depends on -- see playwright.config.ts) removes that race entirely,
// regardless of which order/how many times e2e-frontend and
// e2e-connectivity are invoked: OrgUI.login is a real SSO login + org
// readiness/assignment check, so it's a harmless idempotent re-login (not
// a fresh registration) whenever an identity already exists.
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
})

