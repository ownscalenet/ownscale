import type { Page } from '@playwright/test'

// retryUntil repeats the full save cycle -- `action` (typically a fill +
// click that fires a save request) followed by a page reload and re-check
// of `check` -- until check passes. This local dev stack restarts
// headscale containers as part of saving DNS/auth-restrictions settings,
// and doing so churns docker's bridge/veth interfaces on the host, which
// can cause two distinct kinds of transient failure that both look the
// same from the test's point of view:
//
//   1. Chromium (running directly on the same host as the docker daemon,
//      unlike a real deployment where the browser is on a separate
//      machine) can treat the interface churn as a "network changed"
//      event, aborting the save's own in-flight fetch
//      (net::ERR_NETWORK_CHANGED) -- orgs.OrgService.applyHeadscaleConfig
//      decouples its write/restart tail from the HTTP request's context
//      (a bounded background context instead), so in this case the save
//      actually completes and commits server-side regardless; a reload
//      alone is enough to observe it.
//   2. The restart can also cause a genuine, if rare, backend-side
//      failure (e.g. the org's headscale container being briefly
//      unresolvable via Docker's embedded DNS immediately after
//      restarting), which really does roll back the save -- no amount of
//      reloading will show a change that never landed, so the action
//      needs to be resubmitted.
//
// Because a plain reload can't tell these apart, `action` is always
// re-run before each check (not just once up front) -- it must be safe to
// call repeatedly (e.g. re-filling a field to the same value before
// clicking Save again), which every caller here already is.
export async function retryUntil(
  page: Page,
  action: () => Promise<void>,
  check: () => Promise<void>,
  attempts = 6,
): Promise<void> {
  for (let attempt = 1; attempt <= attempts; attempt++) {
    await action()
    // Give the save request (and the restart it triggers) a moment to
    // land server-side before reloading to check the persisted truth.
    await page.waitForTimeout(700)
    await page.reload()
    try {
      await check()
      return
    } catch (err) {
      if (attempt === attempts) throw err
      // Give the restart more time to fully settle before retrying.
      await page.waitForTimeout(3_000)
    }
  }
}
