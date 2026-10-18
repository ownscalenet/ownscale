import { lookup } from 'node:dns/promises'
import { request } from 'node:https'
import type { Page } from '@playwright/test'
import { expect, test } from './support/account'
import { baseURL } from './connectivity/environment'
import { confirmDialog } from './support/confirm'
import { registerRealMachine, teardownRealMachine, type RealMachine } from './support/realMachine'

// Creates and deletes a funnel route targeting a real registered device.
// CreateFunnelRoute only validates that the machine/port are well-formed --
// it doesn't probe reachability -- so the route lifecycle can be exercised
// without the device actually listening on the target port.

let machine: RealMachine

test('creating and deleting a funnel route', async ({ page, account }) => {
  test.setTimeout(120_000)
  machine = await registerRealMachine(page, account.org)
  try {
    const label = `e2e${Date.now()}`

    await page.goto('/funnels')
    await expect(page.getByRole('heading', { name: 'Funnels', exact: true })).toBeVisible()

    await page.locator('#funnel-label').fill(label)
    await page.locator('#funnel-machine').selectOption({ label: machine.hostname })
    await page.locator('#funnel-port').fill('8080')
    await page.getByRole('button', { name: 'Create' }).click()

    const row = page.getByRole('row').filter({ hasText: machine.hostname })
    // Creating the org's first funnel starts its tsnet client, which may wait
    // up to funnelUpTimeout (45s) for a DERP relay before the route exists.
    await expect(row).toBeVisible({ timeout: 60_000 })
    await expect(row.getByText(`${machine.hostname}:8080`, { exact: true })).toBeVisible()

    await row.getByRole('button', { name: 'Remove' }).click()
    await confirmDialog(page, 'Remove route')
    await expect(row).toBeHidden()
  } finally {
    await teardownRealMachine(machine)
  }
})

// getFunnel GETs a public funnel URL. It connects to the console's address
// with the funnel's name as SNI and Host, so the per-org funnel hostnames
// don't need /etc/hosts entries.
async function getFunnel(hostname: string): Promise<{ status: number; body: string }> {
  const consoleURL = new URL(baseURL)
  const { address } = await lookup(consoleURL.hostname)
  return new Promise((resolve, reject) => {
    const req = request(
      {
        host: address,
        port: consoleURL.port || 443,
        path: '/',
        servername: hostname,
        headers: { Host: hostname },
        rejectUnauthorized: false,
        timeout: 15_000,
      },
      (res) => {
        let body = ''
        res.setEncoding('utf8')
        res.on('data', (chunk: string) => (body += chunk))
        res.on('end', () => resolve({ status: res.statusCode ?? 0, body }))
      },
    )
    req.on('timeout', () => req.destroy(new Error(`GET https://${hostname}/ timed out`)))
    req.on('error', reject)
    req.end()
  })
}

// createFunnel creates a funnel route through the Funnel page and returns
// its public hostname.
async function createFunnel(page: Page, label: string, machineHostname: string, port: number): Promise<string> {
  await page.goto('/funnels')
  await page.locator('#funnel-label').fill(label)
  await page.locator('#funnel-machine').selectOption({ label: machineHostname })
  await page.locator('#funnel-port').fill(String(port))
  const created = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/funnel' && r.request().method() === 'POST', {
    timeout: 60_000,
  })
  await page.getByRole('button', { name: 'Create' }).click()
  const response = await created
  expect(response.ok()).toBe(true)
  return ((await response.json()) as { funnelRoute: { hostname: string } }).funnelRoute.hostname
}

test('a funnel works again after its internal funnel machine is deleted', async ({ page, account }) => {
  test.setTimeout(240_000)
  const funnelMachine = 'ownscale-agent'
  const target = await registerRealMachine(page, account.org)
  try {
    const service = await target.bridge.serve(target.hostname, `${target.hostname}web`, 0)
    const servesTarget = async (hostname: string) => {
      await expect
        .poll(async () => {
          const res = await getFunnel(hostname)
          return res.status === 200 ? (JSON.parse(res.body) as { identity?: string }).identity : `HTTP ${res.status}: ${res.body.trim()}`
        }, { timeout: 30_000 })
        .toBe(service.identity)
    }

    const first = await createFunnel(page, `rc${Date.now()}`, target.hostname, service.port)
    await servesTarget(first)

    // Deleting the internal machine deletes its funnels too.
    await page.goto('/machines')
    const funnelRow = page.getByRole('row').filter({ hasText: funnelMachine })
    await funnelRow.getByRole('button', { name: 'Actions', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Remove', exact: true }).click()
    await confirmDialog(page, 'Delete machine and funnels')
    // The deletion waits for the funnel's tsnet server to close.
    await expect(funnelRow).toBeHidden({ timeout: 30_000 })
    expect((await getFunnel(first)).status).toBe(404)

    // A new funnel registers a new internal machine and reaches the target.
    const second = await createFunnel(page, `rc${Date.now()}`, target.hostname, service.port)
    await servesTarget(second)
    await page.goto('/machines')
    await expect(page.getByRole('row').filter({ hasText: funnelMachine })).toBeVisible()

    await page.goto('/funnels')
    const row = page.getByRole('row').filter({ hasText: second })
    await row.getByRole('button', { name: 'Remove' }).click()
    await confirmDialog(page, 'Remove route')
    await expect(row).toBeHidden()
  } finally {
    await teardownRealMachine(target)
  }
})
