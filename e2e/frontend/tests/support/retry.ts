import type { Page } from '@playwright/test'

// retryUntil repeats the full save cycle -- `action` (typically a fill +
// click that fires a save request) followed by a page reload and re-check
// of `check` -- until check passes. Network transients (e.g.
// net::ERR_NETWORK_CHANGED, brief backend errors) can cause the save's
// in-flight fetch to fail before the DB write commits, so `action` is
// always re-run before each check (not just once up front) -- it must be
// safe to call repeatedly (e.g. re-filling a field to the same value
// before clicking Save again), which every caller here already is.
export async function retryUntil(
  page: Page,
  action: () => Promise<void>,
  check: () => Promise<void>,
  attempts = 6,
): Promise<void> {
  for (let attempt = 1; attempt <= attempts; attempt++) {
    await action()
    // Give the save request (and the restart it triggers) time to land
    // server-side before reloading to check the persisted truth.
    await page.waitForLoadState('networkidle')
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
