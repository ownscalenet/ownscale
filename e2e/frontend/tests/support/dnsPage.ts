import { expect, type Page } from '@playwright/test'

/**
 * dnsPageLoaded waits until the DNS page has loaded its settings. The page
 * renders only a spinner until GET /api/dns returns, so on a slow connection
 * anything read right after a navigation or reload (isVisible(), or an
 * absence check such as toBeHidden()) can see an empty page and skip a step
 * or pass without checking anything.
 */
export async function dnsPageLoaded(page: Page): Promise<void> {
  await expect(page.getByRole('heading', { name: 'DNS', exact: true })).toBeVisible()
}

/**
 * snapshotDns records the org's current DNS settings and returns a function
 * that puts them back if they've changed, through the API so it works even
 * when the page is mid-load or the test failed halfway.
 */
export async function snapshotDns(page: Page): Promise<() => Promise<void>> {
  const read = async () => {
    const response = await page.request.get('/api/dns')
    expect(response.ok()).toBeTruthy()
    return (await response.json()).dns as Record<string, unknown>
  }
  const original = await read()
  return async () => {
    if (JSON.stringify(await read()) === JSON.stringify(original)) return
    const response = await page.request.put('/api/dns', { data: original })
    expect(response.ok(), 'restoring DNS settings').toBeTruthy()
  }
}
