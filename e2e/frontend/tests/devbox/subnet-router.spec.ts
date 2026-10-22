import { randomBytes } from 'node:crypto'
import type { Page } from '@playwright/test'
import { expect, test } from '../support/account'
import {
  createPrivateNetwork, docker, ensureDevboxImage, removeDevbox, removeNetwork, startDevbox, startWebHost,
} from '../support/devbox'
import { getPolicy, putPolicy } from '../support/policy'
import { retryUntil } from '../support/retry'

// A router devbox joins both the tailnet and a private docker network that
// nothing else on the tailnet can reach; a web host lives only on that
// network. The client reaches it only through approved routes.

const allowAll = `{
  "acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],
}`
const exitRoutes = ['0.0.0.0/0', '::/0']

interface Machine {
  id: string
  givenName: string
  ipAddresses: string[]
  user: { name: string; email: string } | null
  availableRoutes: string[] | null
  approvedRoutes: string[] | null
}

async function machine(page: Page, hostname: string): Promise<Machine | undefined> {
  const res = await page.request.get('/api/machines')
  expect(res.ok(), await res.text()).toBe(true)
  const { machines } = (await res.json()) as { machines: Machine[] }
  return machines.find(m => m.givenName === hostname)
}

async function routes(page: Page, hostname: string): Promise<{ available: string[]; approved: string[] }> {
  const m = await machine(page, hostname)
  return { available: [...(m?.availableRoutes ?? [])].sort(), approved: [...(m?.approvedRoutes ?? [])].sort() }
}

test('subnet routers and exit nodes forward traffic only while their routes are approved', async ({ page, account }) => {
  test.setTimeout(15 * 60_000)
  await ensureDevboxImage()

  const suffix = randomBytes(3).toString('hex')
  const lan = `ownscale-e2e-lan-${suffix}`
  const webHost = `ownscale-e2e-lan-web-${suffix}`
  const router = `ownscale-e2e-rtr-${suffix}`
  const router2 = `ownscale-e2e-rtr2-${suffix}`
  const client = `ownscale-e2e-rcli-${suffix}`
  const routerName = `rtr-${suffix}`
  const router2Name = `rtr2-${suffix}`
  const identity = `lan-web-${randomBytes(8).toString('hex')}`
  const routerArgs = ['--sysctl', 'net.ipv4.ip_forward=1', '--sysctl', 'net.ipv6.conf.all.forwarding=1']
  let webIP = ''

  // reach reports whether client gets the web host's identity over HTTP.
  async function reach(): Promise<string> {
    try {
      const out = await docker(['exec', client, 'curl', '-s', '--max-time', '5', `http://${webIP}:8080/`], 20_000)
      return out === identity ? 'reached' : `unexpected response: ${out}`
    } catch {
      return 'unreachable'
    }
  }
  const expectReach = (want: 'reached' | 'unreachable') =>
    expect.poll(reach, { timeout: 120_000, intervals: [2_000] }).toBe(want)

  // editRoutes sets hostname's approved routes through the "Edit route
  // settings" dialog on the machines page.
  async function editRoutes(hostname: string, subnet: string, approveSubnet: boolean, approveExit?: boolean) {
    const row = page.getByRole('row').filter({ has: page.getByText(hostname, { exact: true }) })
    const want = [...(approveSubnet ? [subnet] : []), ...(approveExit ? exitRoutes : [])].sort()
    await retryUntil(
      page,
      async () => {
        await row.getByRole('button', { name: 'Actions', exact: true }).click()
        await page.getByRole('menuitem', { name: 'Edit route settings', exact: true }).click()
        const dialog = page.getByRole('dialog')
        await dialog.getByLabel(subnet).setChecked(approveSubnet)
        if (approveExit !== undefined) await dialog.getByLabel('Use as exit node').setChecked(approveExit)
        await dialog.getByRole('button', { name: 'Save', exact: true }).click()
        await expect(dialog).toBeHidden()
      },
      async () => {
        expect((await routes(page, hostname)).approved).toEqual(want)
      },
    )
  }

  // subnetsCard returns the "Approved routes" and "Awaiting approval"
  // columns of hostname's machine detail page.
  async function subnetsCard(hostname: string) {
    const m = await machine(page, hostname)
    await page.goto(`/machines/${m!.ipAddresses.find(ip => ip.includes('.'))}`)
    const card = page.locator('.card', { hasText: 'Subnets and routing' })
    return {
      approved: card.getByText('Approved routes', { exact: true }).locator('..'),
      awaiting: card.getByText('Awaiting approval', { exact: true }).first().locator('..'),
    }
  }

  const originalPolicy = await getPolicy(page)
  try {
    await putPolicy(page, allowAll)
    const subnet = await createPrivateNetwork(lan)
    expect(subnet).toMatch(/^\d+\.\d+\.\d+\.\d+\/\d+$/)
    webIP = await startWebHost(webHost, lan, identity)
    await startDevbox(router, account.org, routerName, {
      dockerArgs: routerArgs, networks: [lan], upFlags: [`--advertise-routes=${subnet}`],
    })
    await startDevbox(client, account.org, `rcli-${suffix}`, { upFlags: ['--accept-routes'] })

    await test.step('an advertised subnet awaits approval and isn\'t routed', async () => {
      await expect.poll(async () => (await routes(page, routerName)).available, { timeout: 60_000 }).toEqual([subnet])
      const card = await subnetsCard(routerName)
      await expect(card.awaiting.getByText(subnet, { exact: true })).toBeVisible()
      await expect(card.approved.getByText(subnet, { exact: true })).toBeHidden()
      expect(await reach()).toBe('unreachable')
    })

    await test.step('approving the subnet routes traffic to it', async () => {
      await page.goto('/machines')
      await editRoutes(routerName, subnet, true)
      await expectReach('reached')
      const card = await subnetsCard(routerName)
      await expect(card.approved.getByText(subnet, { exact: true })).toBeVisible()
      await expect(card.awaiting.getByText(subnet, { exact: true })).toBeHidden()
    })

    await test.step('unapproving the subnet stops routing it', async () => {
      await page.goto('/machines')
      await editRoutes(routerName, subnet, false)
      await expectReach('unreachable')
    })

    await test.step('an approved exit node forwards traffic, an unapproved one doesn\'t', async () => {
      await docker(['exec', router, 'tailscale', 'set', '--advertise-exit-node'])
      await expect
        .poll(async () => (await routes(page, routerName)).available, { timeout: 60_000 })
        .toEqual([...exitRoutes, subnet].sort())
      await page.goto('/machines')
      const row = page.getByRole('row').filter({ has: page.getByText(routerName, { exact: true }) })
      await expect(row.getByText('Exit node · awaiting approval', { exact: true })).toBeVisible({ timeout: 30_000 })

      await editRoutes(routerName, subnet, false, true)
      await expect(row.getByText('Exit node', { exact: true })).toBeVisible({ timeout: 30_000 })
      const routerIP = await docker(['exec', router, 'tailscale', 'ip', '-4'])
      // The client only accepts it as an exit node once its netmap says so.
      await expect
        .poll(async () => docker(['exec', client, 'tailscale', 'set', `--exit-node=${routerIP}`]).then(() => 'set', () => 'not yet'),
          { timeout: 60_000, intervals: [2_000] })
        .toBe('set')
      // The web host isn't in any approved subnet, so only the exit node
      // (0.0.0.0/0) can carry this traffic.
      await expectReach('reached')

      await editRoutes(routerName, subnet, false, false)
      await expectReach('unreachable')
      await docker(['exec', client, 'tailscale', 'set', '--exit-node='])
    })

    await test.step('autoApprovers approve a new router\'s routes without manual approval', async () => {
      // Log out so headscale deletes the ephemeral node now, leaving router2
      // as the only path to the subnet.
      await docker(['exec', router, 'tailscale', 'logout'])
      await removeDevbox(router)

      const owner = (await machine(page, `rcli-${suffix}`))!.user!
      const approver = owner.email || `${owner.name}@`
      await putPolicy(page, `{
  "acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],
  "autoApprovers": {
    "routes": {"${subnet}": ["${approver}"]},
    "exitNode": ["${approver}"],
  },
}`)
      await startDevbox(router2, account.org, router2Name, {
        dockerArgs: routerArgs, networks: [lan], upFlags: [`--advertise-routes=${subnet}`, '--advertise-exit-node'],
      })
      await expect
        .poll(async () => (await routes(page, router2Name)).approved, { timeout: 60_000 })
        .toEqual([...exitRoutes, subnet].sort())
      const card = await subnetsCard(router2Name)
      await expect(card.approved.getByText(subnet, { exact: true })).toBeVisible()
      await expectReach('reached')
    })
  } finally {
    await removeDevbox(client)
    await removeDevbox(router2)
    await removeDevbox(router)
    await removeDevbox(webHost)
    await removeNetwork(lan)
    await putPolicy(page, originalPolicy)
  }
})
