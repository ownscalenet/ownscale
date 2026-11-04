import { request as playwrightRequest, type Page } from '@playwright/test'
import { expect, storageStateOf, test } from './support/account'
import { contentDomain, localTLS } from './connectivity/environment'
import { dnsPageLoaded, snapshotDns } from './support/dnsPage'
import { retryUntil } from './support/retry'

// The tailnet name is the MagicDNS base domain shown on the DNS page,
// "<label>.os.<content domain>". It is not the org's login URL
// (<subdomain>.<base domain>), which is where tailscale clients sign in.

const hostname = /^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]*[a-z0-9])?$/

function shownTailnetName(page: Page) {
  return page.locator('.card', { hasText: 'Tailnet Name' }).locator('.font-mono')
}

async function apiTailnetName(page: Page): Promise<string> {
  const response = await page.request.get('/api/dns')
  expect(response.ok()).toBeTruthy()
  return (await response.json()).dns.baseDomain as string
}

test.describe('tailnet name', () => {
  test('is shown on the DNS page, matches the API and is not the login URL', async ({ page, account }) => {
    await page.goto('/dns')
    await dnsPageLoaded(page)

    const shown = (await shownTailnetName(page).textContent())?.trim() ?? ''
    expect(shown).toMatch(hostname)
    expect(shown).toBe(await apiTailnetName(page))

    const loginURLHost = `${account.org}.${contentDomain}`
    expect(shown).not.toBe(loginURLHost)
    expect(shown).not.toMatch(new RegExp(`^${account.org}\\.`))
    expect(shown.endsWith(`.os.${contentDomain}`)).toBe(true)
  })

  test('is different in every organization', async ({ page, account, baseURL }) => {
    const other = account.name === 'ssh' ? 'worker0' : 'ssh'
    const otherAPI = await playwrightRequest.newContext({
      baseURL,
      storageState: storageStateOf(other),
      ignoreHTTPSErrors: localTLS,
    })
    try {
      const response = await otherAPI.get('/api/dns')
      expect(response.ok()).toBeTruthy()
      const otherName = (await response.json()).dns.baseDomain as string
      expect(otherName).toMatch(hostname)
      expect(otherName).not.toBe(await apiTailnetName(page))
    } finally {
      await otherAPI.dispose()
    }
  })

  test('survives reloads and DNS changes, and cannot be set by the client', async ({ page }) => {
    // Every save restarts the org's headscale, so allow for retries.
    test.setTimeout(300_000)
    await page.goto('/dns')
    await dnsPageLoaded(page)
    const name = await apiTailnetName(page)
    const restore = await snapshotDns(page)
    try {
      await page.reload()
      await dnsPageLoaded(page)
      await expect(shownTailnetName(page)).toHaveText(name)

      const read = async () => {
        const response = await page.request.get('/api/dns')
        expect(response.ok()).toBeTruthy()
        return (await response.json()).dns as Record<string, any>
      }
      const save = async (change: (dns: Record<string, any>) => void) => {
        const dns = await read()
        change(dns)
        let status = 0
        await retryUntil(
          page,
          async () => {
            status = (await page.request.put('/api/dns', { data: dns })).status()
          },
          async () => expect(status).toBe(200),
        )
        expect(await apiTailnetName(page)).toBe(name)
      }

      const suffix = Date.now()
      await save(dns => { dns.nameservers.global = [...dns.nameservers.global, `10.${suffix % 200}.0.1`] })
      await save(dns => { dns.searchDomains = [...dns.searchDomains, `internal-${suffix}.example.com`] })
      await save(dns => {
        dns.extraRecords = [...dns.extraRecords, { name: `e2e-${suffix}.example.com`, type: 'A', value: '10.0.0.42' }]
      })
      await save(dns => { dns.nameservers.split = { ...dns.nameservers.split, [`split-${suffix}.example.com`]: ['10.0.0.53'] } })
      await save(dns => { dns.magicDns = !dns.magicDns })
      await save(dns => { dns.magicDns = !dns.magicDns })
      // A client-supplied base domain is ignored.
      await save(dns => { dns.baseDomain = `other-${suffix}.example.com` })

      await page.reload()
      await dnsPageLoaded(page)
      await expect(shownTailnetName(page)).toHaveText(name)
    } finally {
      await restore()
    }
  })
})
