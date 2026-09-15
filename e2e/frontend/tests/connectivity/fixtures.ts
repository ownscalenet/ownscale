import { randomBytes } from 'node:crypto'
import path from 'node:path'
import { test as base } from '@playwright/test'
import { Bridge, type NodeInfo, type ServiceInfo } from './bridge'
import { baseDomain, enrollmentTimeoutMs, privateDir } from './environment'
import { OrgUI, identities } from './ui'
import { Recovery, type OwnedResource } from './recovery'

// Playwright's automatic error-context DOM snapshots can include one-time keys or ACL text.
process.env.PLAYWRIGHT_NO_COPY_PROMPT = '1'

export interface Endpoint {
  ui: OrgUI
  info: NodeInfo
  service: ServiceInfo
  resource: OwnedResource
}

export class Harness {
  readonly endpoints: Endpoint[] = []
  readonly observations: object[] = []
  constructor(readonly orgs: OrgUI[], readonly bridge: Bridge, readonly recovery: Recovery) {}

  async enroll(ui: OrgUI, index: number): Promise<Endpoint> {
    const hostname = `ct${this.recovery.runID}${ui.org}${index}`
    const { machines } = await ui.read<{ machines: { name: string; givenName: string }[] }>('/api/machines')
    if (machines.some(m => m.name === hostname || m.givenName === hostname)) throw new Error(`Refusing existing machine hostname ${hostname}`)
    const authKey = await ui.createKey(this.recovery, `${hostname}key`)
    const resource: OwnedResource = { token: hostname, org: ui.org, kind: 'machine', reference: hostname }
    await this.recovery.own(resource)
    const info = await this.bridge.create({
      node: hostname, hostname, controlURL: `https://${ui.org}.${baseDomain}`, authKey,
    }, enrollmentTimeoutMs)
    await this.recovery.update(resource, { addresses: info.addresses })
    await ui.discover(resource, this.recovery, enrollmentTimeoutMs)
    const service = await this.bridge.serve(info.node, `${hostname}http`, 18080)
    const endpoint = { ui, info, service, resource }
    this.endpoints.push(endpoint)
    this.observations.push({ phase: 'enrolled', org: ui.org, hostname, machineID: resource.id, addresses: info.addresses, backendState: info.backendState })
    return endpoint
  }
}

export const test = base.extend<{ harness: Harness }, { orgs: OrgUI[] }>({
  orgs: [async ({ browser }, use) => {
    await Recovery.assertClean(path.join(privateDir, 'recovery'))
    const orgs: OrgUI[] = []
    try {
      // Validate all three registrations before any key, policy, node or route mutation.
      for (const identity of identities) orgs.push(await OrgUI.login(browser, identity))
      await orgs[0].assertFunnelUnused()
      await use(orgs)
    } finally {
      const results = await Promise.allSettled(orgs.map(org => org.context.close()))
      if (results.some(result => result.status === 'rejected')) throw new Error('Failed to close an authenticated browser context')
    }
  }, { scope: 'worker', timeout: 17 * 60_000 }],

  harness: [async ({ orgs }, use, testInfo) => {
    const recovery = await Recovery.create(path.join(privateDir, 'recovery'), randomBytes(6).toString('hex'))
    let bridge: Bridge | undefined
    let harness: Harness | undefined
    const cleanupErrors: string[] = []
    const attempt = async (label: string, action: () => Promise<unknown>) => {
      try { await action() } catch (err) {
        const detail = err instanceof Error ? err.message : String(err)
        cleanupErrors.push(`${label}: ${detail}`)
      }
    }
    try {
      bridge = await Bridge.start()
      harness = new Harness(orgs, bridge, recovery)
      await use(harness)
    } finally {
      const deadline = Date.now() + 280_000
      const uiFor = (org: string) => orgs.find(ui => ui.org === org)!
      for (const ui of orgs) {
        ui.page.setDefaultTimeout(5000)
        ui.page.setDefaultNavigationTimeout(5000)
      }
      for (const route of [...recovery.resources].filter(r => r.kind === 'route')) {
        await attempt(`remove owned route ${route.org}/${route.id ?? route.reference}`, () => uiFor(route.org).removeResource(route, recovery))
      }
      if (bridge) await attempt('graceful bridge shutdown (all owned nodes/services)', () => bridge!.shutdown())
      await Promise.all(orgs.map(async ui => {
        for (const kind of ['key', 'machine'] as const) {
          for (const resource of [...recovery.resources].filter(r => r.org === ui.org && r.kind === kind)) {
            await attempt(`remove/expire owned ${kind} ${resource.org}/${resource.id ?? resource.reference}`, () => ui.removeResource(resource, recovery))
          }
        }
      }))
      await Promise.all([...recovery.policies].map(async snapshot => {
        await attempt(`restore ${snapshot.org} ACL and confirm headscale readiness`, async () => {
          await uiFor(snapshot.org).savePolicy(snapshot.original, Math.max(1000, Math.min(60_000, deadline - Date.now())))
          await recovery.restored(snapshot.org)
        })
      }))
      if (!cleanupErrors.length) await attempt(`finish private recovery journal ${recovery.file}`, () => recovery.finish())
      if (harness) await testInfo.attach('connectivity-observations', {
        body: JSON.stringify(harness.observations, null, 2), contentType: 'application/json',
      })
      if (bridge) await testInfo.attach('bridge-diagnostics', { body: bridge.diagnostics(), contentType: 'text/plain' })
      if (cleanupErrors.length) {
        const message = `Connectivity cleanup failures (original test errors remain above):\n${cleanupErrors.join('\n')}\nPrivate recovery: ${recovery.file}`
        await testInfo.attach('cleanup-failures', { body: message, contentType: 'text/plain' })
        throw new Error(message)
      }
    }
  }, { timeout: 5 * 60_000 }],
})

export { expect } from '@playwright/test'
