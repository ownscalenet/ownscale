import { randomBytes } from 'node:crypto'
import type { Page } from '@playwright/test'
import { expect, test } from '../support/account'
import { docker, ensureDevboxImage, removeDevbox, startDevbox } from '../support/devbox'
import { dnsPageLoaded, snapshotDns } from '../support/dnsPage'
import { retryUntil } from '../support/retry'

// answerIP is what dnsmasq on the server devbox answers; it's never routed,
// only compared against the client's lookup result.
const answerIP = '10.53.0.7'

const allowAll = `{
  "acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],
}`

async function getPolicy(page: Page): Promise<string> {
  const res = await page.request.get('/api/access-control')
  expect(res.ok(), await res.text()).toBe(true)
  return ((await res.json()) as { content: string }).content
}

async function putPolicy(page: Page, content: string): Promise<void> {
  const res = await page.request.put('/api/access-control', { data: { content } })
  expect(res.ok(), await res.text()).toBe(true)
}

test('split DNS sends queries for a domain to its nameserver on the tailnet', async ({ page, account }) => {
  test.setTimeout(10 * 60_000)
  await ensureDevboxImage()

  const suffix = randomBytes(3).toString('hex')
  const server = `ownscale-e2e-dns-srv-${suffix}`
  const client = `ownscale-e2e-dns-cli-${suffix}`
  const domain = `split-${suffix}.example.com`
  const host = `db.${domain}`

  // lookup resolves host on client through tailscaled's own resolver
  // (100.100.100.100), which is where split DNS routes live, and reports
  // whether it got answerIP.
  async function lookup(): Promise<string> {
    try {
      const out = await docker(['exec', client, 'tailscale', 'dns', 'query', host], 20_000)
      return out.includes(answerIP) ? 'resolved' : `not resolved: ${out}`
    } catch (err) {
      const stderr = (err as { stderr?: string }).stderr?.trim()
      return `failed: ${stderr || String(err)}`
    }
  }

  await page.goto('/dns')
  await dnsPageLoaded(page)
  const restoreDns = await snapshotDns(page)
  const originalPolicy = await getPolicy(page)
  try {
    await putPolicy(page, allowAll)
    await startDevbox(server, account.org, `dns-srv-${suffix}`)
    await startDevbox(client, account.org, `dns-cli-${suffix}`)
    const serverIP = await docker(['exec', server, 'tailscale', 'ip', '-4'])
    await docker([
      'exec', '-d', server, 'dnsmasq', '--keep-in-foreground', '--no-resolv', '--no-hosts',
      `--listen-address=${serverIP}`, '--bind-interfaces', `--address=/${domain}/${answerIP}`,
    ])

    expect(await lookup()).not.toBe('resolved')

    const row = page.locator('.card', { hasText: 'Split DNS' }).getByRole('row').filter({ hasText: domain })
    await retryUntil(
      page,
      async () => {
        await dnsPageLoaded(page)
        if (await row.isVisible()) return
        await page.getByPlaceholder('Domain (e.g. corp.example.com)').fill(domain)
        await page.getByPlaceholder('Nameservers (e.g. 10.0.0.53, 10.0.0.54)').fill(serverIP)
        await page.locator('.card', { hasText: 'Split DNS' }).getByRole('button', { name: 'Add' }).click()
      },
      async () => {
        await expect(row).toBeVisible({ timeout: 8_000 })
      },
    )
    await expect.poll(lookup, { timeout: 120_000, intervals: [2_000] }).toBe('resolved')

    await retryUntil(
      page,
      async () => {
        await dnsPageLoaded(page)
        if (!(await row.isVisible())) return
        await row.getByRole('button', { name: 'Remove' }).click()
      },
      async () => {
        await dnsPageLoaded(page)
        await expect(row).toBeHidden({ timeout: 8_000 })
      },
    )
    await expect
      .poll(async () => ((await lookup()) === 'resolved' ? 'resolved' : 'not resolved'), {
        timeout: 120_000,
        intervals: [2_000],
      })
      .toBe('not resolved')
  } finally {
    await removeDevbox(client)
    await removeDevbox(server)
    await restoreDns()
    await putPolicy(page, originalPolicy)
  }
})
