import { randomBytes } from 'node:crypto'
import { expect, test, type Page } from '@playwright/test'
import { baseDomain } from './connectivity/environment'
import { registerRealMachine, teardownRealMachine } from './support/realMachine'
import { dnsPageLoaded, snapshotDns } from './support/dnsPage'
import { retryUntil } from './support/retry'

// Saving settings restarts the organization's headscale from its stored
// config. These specs check the settings actually take effect *inside*
// headscale, from the outside: what a real enrolled device receives from the
// control server, and how headscale's login endpoint treats a signed-in user.

const org = 'aaaa'

test('DNS settings reach a running device', async ({ page }) => {
  test.setTimeout(300_000)
  const machine = await registerRealMachine(page, org)
  let restore = async () => {}
  try {
    const dns = () => machine.bridge.dns(machine.hostname)
    const converge = { timeout: 90_000, intervals: [2_000] }

    const suffix = Date.now()
    const nameserver = `10.${suffix % 200}.0.1`
    const searchDomain = `internal-${suffix}.example.com`
    const recordName = `e2e-${suffix}.example.com`

    await page.goto('/dns')
    await dnsPageLoaded(page)
    restore = await snapshotDns(page)

    const nameserverBadge = page.locator('.badge', { hasText: nameserver })
    await retryUntil(
      page,
      async () => {
        await dnsPageLoaded(page)
        if (await nameserverBadge.isVisible()) return
        await page.getByPlaceholder('1.1.1.1').fill(nameserver)
        await page.getByRole('button', { name: 'Add' }).first().click()
      },
      async () => expect(nameserverBadge).toBeVisible({ timeout: 8_000 }),
    )

    const searchDomainBadge = page.locator('.badge', { hasText: searchDomain })
    await retryUntil(
      page,
      async () => {
        await dnsPageLoaded(page)
        if (await searchDomainBadge.isVisible()) return
        await page.getByPlaceholder('internal.example.com').fill(searchDomain)
        await page.locator('.card', { hasText: 'Search Domains' }).getByRole('button', { name: 'Add' }).click()
      },
      async () => expect(searchDomainBadge).toBeVisible({ timeout: 8_000 }),
    )

    const recordRow = page.getByRole('row').filter({ hasText: recordName })
    await retryUntil(
      page,
      async () => {
        await dnsPageLoaded(page)
        if (await recordRow.isVisible()) return
        await page.getByPlaceholder('Name (e.g. server.example.com)').fill(recordName)
        await page.getByPlaceholder('Value (e.g. 10.0.0.1)').fill('10.0.0.42')
        await page.locator('.card', { hasText: 'DNS Records' }).getByRole('button', { name: 'Add' }).click()
      },
      async () => expect(recordRow).toBeVisible({ timeout: 8_000 }),
    )

    // The device's netmap only changes once headscale has restarted with the
    // new config and the device has reconnected to it.
    await expect.poll(async () => {
      const got = await dns()
      return {
        nameserver: [...got.resolvers, ...got.fallbackResolvers].includes(nameserver),
        searchDomain: got.domains.includes(searchDomain),
        record: got.extraRecords.some(r => r.name === recordName && r.value === '10.0.0.42'),
      }
    }, converge).toEqual({ nameserver: true, searchDomain: true, record: true })

    // Remove everything again so repeat runs start from the same state.
    await retryUntil(
      page,
      async () => {
        await dnsPageLoaded(page)
        if (!(await nameserverBadge.isVisible())) return
        await page.getByRole('button', { name: `Remove ${nameserver}` }).click()
      },
      async () => {
        await dnsPageLoaded(page)
        await expect(nameserverBadge).toBeHidden({ timeout: 8_000 })
      },
    )
    await retryUntil(
      page,
      async () => {
        await dnsPageLoaded(page)
        if (!(await searchDomainBadge.isVisible())) return
        await page.getByRole('button', { name: `Remove ${searchDomain}` }).click()
      },
      async () => {
        await dnsPageLoaded(page)
        await expect(searchDomainBadge).toBeHidden({ timeout: 8_000 })
      },
    )
    await retryUntil(
      page,
      async () => {
        await dnsPageLoaded(page)
        if (!(await recordRow.isVisible())) return
        await recordRow.getByRole('button', { name: 'Remove' }).click()
      },
      async () => {
        await dnsPageLoaded(page)
        await expect(recordRow).toBeHidden({ timeout: 8_000 })
      },
    )

    await expect.poll(async () => {
      const got = await dns()
      return [
        ...got.resolvers, ...got.fallbackResolvers, ...got.domains, ...got.extraRecords.map(r => r.name),
      ].filter(v => [nameserver, searchDomain, recordName].includes(v))
    }, converge).toEqual([])

    // MagicDNS on/off is delivered as the netmap's DNS "proxied" flag.
    const magicDnsToggle = page.locator('input[type="checkbox"].toggle')
    const wasEnabled = await magicDnsToggle.isChecked({ timeout: 8_000 })
    const flip = async (enabled: boolean) => {
      await retryUntil(
        page,
        async () => {
          if ((await magicDnsToggle.isChecked({ timeout: 8_000 })) === enabled) return
          await magicDnsToggle.click()
        },
        async () => expect(magicDnsToggle).toBeChecked({ checked: enabled, timeout: 8_000 }),
      )
      await expect.poll(async () => (await dns()).magicDns, converge).toBe(enabled)
    }
    try {
      await flip(!wasEnabled)
    } finally {
      await flip(wasEnabled)
    }
  } finally {
    try {
      await restore()
    } finally {
      await teardownRealMachine(machine)
    }
  }
})

// Headscale checks the OIDC allowlist in its /oidc/callback before it looks
// up the pending registration. Starting a registration login with an auth ID
// nobody is waiting on therefore ends in 401 when the signed-in user is
// excluded by the allowlist, and in 410 ("login session expired") when the
// user is admitted -- without needing a real device to register. The console
// finishes its part of the login client-side, so the outcome is the status of
// the callback navigation, not of the first page load.
async function registerLoginStatus(page: Page): Promise<number> {
  // A separate tab (sharing the signed-in session), so the console's
  // client-side redirects can't interfere with the page the test drives.
  const tab = await page.context().newPage()
  try {
    const authID = `hskey-authreq-${randomBytes(12).toString('hex')}`
    const callback = tab.waitForResponse(
      r => r.request().isNavigationRequest() && r.url().startsWith(`https://${org}.${baseDomain}/oidc/callback`),
      { timeout: 30_000 },
    )
    // goto itself may report "interrupted by another navigation" as the
    // console redirects client-side; only the callback's status matters.
    void tab.goto(`https://${org}.${baseDomain}/register/${authID}`).catch(() => {})
    return (await callback).status()
  } finally {
    void tab.close().catch(() => {})
  }
}

test('login restrictions are enforced by headscale', async ({ page }) => {
  test.setTimeout(240_000)
  const converge = { timeout: 90_000, intervals: [2_000] }

  await page.goto('/auth-restrictions')
  await expect(page.getByRole('heading', { name: 'Authentication Restrictions' })).toBeVisible()
  const editor = page.locator('textarea')
  const original = await editor.inputValue()

  // Headscale creates a user for an OIDC login it doesn't already know; the
  // logins below must reuse the person's existing headscale user instead.
  const headscaleUsers = async () => {
    const response = await page.request.get('/api/auth-keys')
    const body = await response.json() as { users: { name: string }[] }
    return body.users.map(u => u.name).sort()
  }
  const usersBefore = await headscaleUsers()

  // Baseline: the current allowlist admits this user.
  await expect.poll(() => registerLoginStatus(page), converge).toBe(410)

  const save = async (content: string, marker: string) => {
    await page.goto('/auth-restrictions')
    await retryUntil(
      page,
      async () => {
        await editor.fill(content)
        await page.getByRole('button', { name: 'Save' }).click()
      },
      async () => expect(editor).toHaveValue(new RegExp(marker), { timeout: 8_000 }),
    )
  }

  try {
    await save('{"domains": ["example.org"], "groups": [], "users": []}', 'example\\.org')
    await expect.poll(() => registerLoginStatus(page), converge).toBe(401)
  } finally {
    await page.goto('/auth-restrictions')
    await retryUntil(
      page,
      async () => {
        await editor.fill(original)
        await page.getByRole('button', { name: 'Save' }).click()
      },
      async () => expect(editor).not.toHaveValue(/example\.org/, { timeout: 8_000 }),
    )
  }
  await expect.poll(() => registerLoginStatus(page), converge).toBe(410)

  expect(await headscaleUsers()).toEqual(usersBefore)
})
