import { expect, type Browser, type BrowserContext, type Page } from '@playwright/test'
import type {
  AccessControlResponse, AuthKeysResponse, FunnelRoute, FunnelRoutesResponse,
  Machine, MachinesResponse, User,
} from '../../../../frontend/src/api'
import { baseDomain, baseURL } from './environment'
import { Recovery, type OwnedResource } from './recovery'
import { confirmDialog } from '../support/confirm'

export const identities = [
  { name: 'admin', email: 'admin@ownscale.test', org: 'aaaa' },
  { name: 'user1', email: 'user1@ownscale.test', org: 'bbbb' },
  { name: 'user2', email: 'user2@ownscale.test', org: 'cccc' },
] as const

type Identity = typeof identities[number]
interface Session {
  user: User
  organization: { Domain: string; Status: string } | null
}

class Transitional extends Error {}

/** poll retries explicit transitional observations, not arbitrary failures. */
export async function poll<T>(
  phase: string, timeoutMs: number, observe: () => Promise<T | undefined>,
): Promise<T> {
  const deadline = Date.now() + timeoutMs
  let last = 'not yet observed'
  while (Date.now() < deadline) {
    try {
      const value = await observe()
      if (value !== undefined) return value
      last = 'waiting for convergence'
    } catch (error) {
      if (!(error instanceof Transitional)) throw error
      last = error.message
    }
    await new Promise(resolve => setTimeout(resolve, Math.min(1000, Math.max(0, deadline - Date.now()))))
  }
  throw new Error(`${phase} timed out after ${timeoutMs}ms; last state: ${last}`)
}

/** validateSession fails immediately on wrong identities, mappings, or permissions. */
export function validateSession(session: Session, identity: Identity): boolean {
  if (session.user?.Email !== identity.email) throw new Error(`${identity.org}: unexpected signed-in user`)
  if (!['owner', 'admin'].includes(session.user.Role)) throw new Error(`${identity.org}: owner/admin permission required`)
  if (!session.organization) throw new Error(`${identity.org}: signed-in user has no assigned organization`)
  if (session.organization.Domain !== `${identity.org}.${baseDomain}`) {
    throw new Error(`${identity.name} must belong to ${identity.org}.${baseDomain}; unexpected registration assignment. Do not reassign or delete existing users.`)
  }
  // update_pending: the org's headscale is restarting to apply saved settings.
  if (!['active', 'update_pending'].includes(session.organization.Status)) throw new Error(`${identity.org}: unknown organization status`)
  return session.organization.Status === 'active'
}

// stepTimeoutMs bounds each UI step of saving a policy. Saves take up to
// about a second, but a busy stack (other suites running) or a remote one
// can be several times slower.
const stepTimeoutMs = 10_000

/** OrgUI mutates resources only through the authenticated browser. */
export class OrgUI {
  ownerID = ''
  private constructor(readonly identity: Identity, readonly context: BrowserContext, readonly page: Page) {}
  get org(): string { return this.identity.org }

  static async login(browser: Browser, identity: Identity): Promise<OrgUI> {
    const context = await browser.newContext({ baseURL, ignoreHTTPSErrors: true })
    const page = await context.newPage()
    const ui = new OrgUI(identity, context, page)
    const deadline = Date.now() + 300_000
    const remaining = () => Math.max(1, deadline - Date.now())
    page.setDefaultTimeout(15_000)
    page.setDefaultNavigationTimeout(remaining())
    try {
      try {
        await page.goto('/login', { timeout: remaining() })
        await page.getByRole('link', { name: 'Sign in with SSO' }).click({ timeout: remaining() })
        await page.locator('#login').fill(identity.email, { timeout: remaining() })
        await page.locator('#password').fill('ownscale', { timeout: remaining() })
        await page.locator('#submit-login').click({ timeout: remaining(), noWaitAfter: true })
        await page.waitForURL(url => url.origin === new URL(baseURL).origin && !url.pathname.startsWith('/auth/'), { timeout: remaining() })
      } catch {
        throw new Error(`${identity.org}: real Dex login/callback failed within the five-minute provisioning budget; check seeded credentials and provisioning logs`)
      }
      await ui.ready(remaining())
      page.setDefaultNavigationTimeout(30_000)
      return ui
    } catch (error) {
      await context.close()
      throw error
    }
  }

  async read<T>(endpoint: string, timeoutMs = 10_000): Promise<T> {
    let response
    try {
      response = await this.context.request.get(endpoint, { timeout: timeoutMs, maxRedirects: 0 })
    } catch (error) {
      const message = error instanceof Error ? error.message : ''
      if (/ECONNRESET|ECONNREFUSED|ETIMEDOUT|Timeout .*exceeded|socket hang up/.test(message)) {
        throw new Transitional(`${endpoint}: transient network failure`)
      }
      throw new Error(`${this.org}: ${endpoint} request failed`)
    }
    try {
      if ([409, 502, 503, 504].includes(response.status())) {
        throw new Transitional(`${endpoint}: HTTP ${response.status()}`)
      }
      if (!response.ok()) throw new Error(`${this.org}: ${endpoint} HTTP ${response.status()}; check authentication, authorization and server health`)
      try { return await response.json() as T } catch { throw new Error(`${endpoint}: invalid JSON response`) }
    } finally { await response.dispose() }
  }

  async ready(timeoutMs = 90_000): Promise<void> {
    const deadline = Date.now() + timeoutMs
    await poll(`${this.org} session/headscale readiness`, timeoutMs, async () => {
      const budget = () => Math.max(1, Math.min(10_000, deadline - Date.now()))
      const session = await this.read<Session>('/api/session', budget())
      if (!validateSession(session, this.identity)) return undefined
      await this.read<MachinesResponse>('/api/machines', budget())
      const keys = await this.read<AuthKeysResponse>('/api/auth-keys', budget())
      const members = await this.read<{ canManage: boolean }>('/api/members', budget())
      if (!members.canManage) throw new Error(`${this.org}: manager permission required`)
      const owner = keys.assignableUsers.find(user => user.email === this.identity.email)
      if (!owner) return undefined
      this.ownerID = String(owner.id)
      return true
    })
  }

  async policy(): Promise<string> {
    const value = await this.read<AccessControlResponse>('/api/access-control')
    if (!value.canManage || typeof value.content !== 'string') throw new Error(`${this.org}: ACL management unavailable`)
    return value.content
  }

  async savePolicy(content: string, timeoutMs = 90_000): Promise<void> {
    let phase = 'navigation'
    try {
      await this.page.goto('/access-control', { timeout: stepTimeoutMs })
      const editor = this.page.locator('textarea')
      phase = 'editor readiness'
      await editor.waitFor({ timeout: stepTimeoutMs })
      await expect(this.page.getByRole('heading', { name: 'Access Control', exact: true })).toBeVisible({ timeout: stepTimeoutMs })
      if (await editor.inputValue() !== content) {
        phase = 'editor input'
        await editor.fill(content, { timeout: stepTimeoutMs })
        phase = 'save request'
        const [response] = await Promise.all([
          this.page.waitForResponse(r => new URL(r.url()).pathname === '/api/access-control' && r.request().method() === 'PUT', { timeout: stepTimeoutMs }),
          this.page.getByRole('button', { name: 'Save', exact: true }).click({ timeout: stepTimeoutMs }),
        ])
        phase = `save response HTTP ${response.status()}`
        if (!response.ok()) throw new Error('ACL save rejected')
        phase = 'save confirmation'
        await expect(this.page.getByText('Policy saved. Headscale is restarting to apply it.')).toBeVisible({ timeout: stepTimeoutMs })
      }
    } catch (err) {
      const detail = err instanceof Error ? err.message : String(err)
      throw new Error(`${this.org}: ACL ${phase} failed; private snapshot retained until verified restoration: ${detail}`)
    }
    await this.ready(5_000)
    if (await this.policy() !== content) throw new Error(`${this.org}: ACL save did not round-trip; private snapshot retained`)
  }

  async createKey(recovery: Recovery, token: string): Promise<string> {
    const resource: OwnedResource = { token, org: this.org, kind: 'key', reference: token }
    await this.page.goto('/settings')
    await this.page.getByRole('button', { name: 'Create pre-auth key' }).click()
    const dialog = this.page.getByRole('dialog')
    await dialog.getByRole('combobox').selectOption(this.ownerID)
    await dialog.getByLabel('Reusable', { exact: true }).uncheck()
    await dialog.getByLabel('Ephemeral', { exact: true }).check()
    await dialog.locator('input[type="number"]').fill('1')
    await recovery.own(resource)
    try {
      const responsePromise = this.page.waitForResponse(r => new URL(r.url()).pathname === '/api/auth-keys' && r.request().method() === 'POST')
      await dialog.getByRole('button', { name: 'Create', exact: true }).click()
      const response = await responsePromise
      if (!response.ok()) {
        if (response.status() < 500) await recovery.released(resource)
        throw new Error('Key rejected')
      }
      const { authKey } = await response.json()
      if (!authKey?.id) throw new Error('Missing key ownership ID')
      await recovery.update(resource, { id: String(authKey.id) })
      if (authKey.reusable || !authKey.ephemeral || String(authKey.user?.id) !== this.ownerID) throw new Error('Wrong key properties')
      await dialog.getByText('Pre-auth key created', { exact: true }).waitFor()
      const key = await dialog.locator('pre code').first().textContent()
      if (!key || key !== authKey.key) throw new Error('One-time key unavailable')
      await dialog.getByRole('button', { name: 'Done' }).click()
      return key
    } catch {
      throw new Error(`${this.org}: owner-owned ephemeral non-reusable key creation failed; consult private ownership record (no key logged)`)
    }
  }

  async discover(resource: OwnedResource, recovery: Recovery, timeoutMs = 90_000): Promise<Machine> {
    const machine = await poll(`${this.org}/${resource.reference} machine registration`, timeoutMs, async () => {
      const { machines } = await this.read<MachinesResponse>('/api/machines')
      const matches = machines.filter(m => m.name === resource.reference || m.givenName === resource.reference)
      if (matches.length > 1) throw new Error(`${this.org}: ambiguous machine hostname`)
      const found = matches[0]
      if (!found) return undefined
      if (resource.addresses?.length && !resource.addresses.every(address => found.ipAddresses.includes(address))) {
        throw new Error(`${this.org}: hostname/address mismatch; refusing to adopt machine`)
      }
      await recovery.update(resource, { id: String(found.id) })
      return found.online ? found : undefined
    })
    await this.page.goto('/machines')
    const row = this.page.getByRole('row').filter({ has: this.page.getByText(machine.givenName, { exact: true }) })
    await expect(row.getByText('Connected', { exact: true })).toBeVisible()
    return machine
  }

  async assertFunnelUnused(): Promise<void> {
    const routes = await this.read<FunnelRoutesResponse>('/api/funnel')
    if (!routes.canManage) throw new Error(`${this.org}: funnel manager permission required`)
    if (routes.funnelRoutes.some(route => route.label === 'web2')) {
      throw new Error(`${this.org}: web2 already exists; remove it explicitly or use a clean prerequisite. It will not be overwritten.`)
    }
  }

  async createFunnel(machineID: string, port: number, recovery: Recovery): Promise<FunnelRoute> {
    await this.assertFunnelUnused()
    await this.page.goto('/funnel')
    await this.page.locator('#funnel-label').fill('web2')
    await this.page.locator('#funnel-machine').selectOption(machineID)
    await this.page.locator('#funnel-port').fill(String(port))
    const resource: OwnedResource = { token: `${recovery.runID}-funnel`, org: this.org, kind: 'route', reference: `web2:${machineID}:${port}` }
    await recovery.own(resource)
    const responsePromise = this.page.waitForResponse(r => new URL(r.url()).pathname === '/api/funnel' && r.request().method() === 'POST')
    await this.page.getByRole('button', { name: 'Create', exact: true }).click()
    const response = await responsePromise
    if (!response.ok()) {
      if (response.status() < 500) await recovery.released(resource)
      throw new Error(`${this.org}: funnel creation rejected (HTTP ${response.status()})`)
    }
    const { funnelRoute: route } = await response.json() as { funnelRoute: FunnelRoute }
    if (!route.id) throw new Error(`${this.org}: missing created funnel ID`)
    await recovery.update(resource, { id: String(route.id) })
    if (route.hostname !== `web2-${this.org}.${baseDomain}` || String(route.targetNodeId) !== machineID || route.targetPort !== port) {
      throw new Error(`${this.org}: created funnel metadata mismatch`)
    }
    await expect(this.page.getByRole('link', { name: `https://${route.hostname}`, exact: true })).toBeVisible()
    return route
  }

  async removeResource(resource: OwnedResource, recovery: Recovery): Promise<void> {
    if (resource.kind === 'key') {
      if (!resource.id) throw new Error(`${this.org}: indeterminate key creation ${resource.token}; manual recovery required`)
      const { authKeys } = await this.read<AuthKeysResponse>('/api/auth-keys')
      const key = authKeys.find(k => String(k.id) === resource.id)
      if (key && !(key.used && !key.reusable) && !(new Date(key.expiration).getTime() <= Date.now())) {
        try {
          await this.page.goto('/settings')
          await this.page.getByRole('heading', { name: 'Pre-Auth Keys' }).waitFor()
          const rows = this.page.locator('tbody tr')
          // Keep key material out of locator descriptions and assertion reports.
          const index = await rows.evaluateAll((elements, text) => {
            const matches = elements.map((element, index) => element.children[0]?.textContent === text ? index : -1).filter(index => index >= 0)
            return matches.length === 1 ? matches[0] : -1
          }, key.key)
          if (index < 0) throw new Error('Key row missing')
          await rows.nth(index).getByRole('button', { name: 'Expire Key' }).click()
          await confirmDialog(this.page, 'Expire key')
          await poll(`${this.org} key expiration`, 15_000, async () => {
            const observed = (await this.read<AuthKeysResponse>('/api/auth-keys')).authKeys.find(k => String(k.id) === resource.id)
            return !observed || (observed.used && !observed.reusable) || new Date(observed.expiration).getTime() <= Date.now() ? true : undefined
          })
        } catch { throw new Error(`${this.org}: expiration of owned key ID ${resource.id} failed`) }
      }
    } else if (resource.kind === 'machine') {
      const machines = (await this.read<MachinesResponse>('/api/machines')).machines
      const matches = machines.filter(m => resource.id ? String(m.id) === resource.id : m.name === resource.reference || m.givenName === resource.reference)
      if (matches.length > 1) throw new Error(`${this.org}: ambiguous owned machine ${resource.reference}`)
      const machine = matches[0]
      if (machine) {
        if ((machine.name !== resource.reference && machine.givenName !== resource.reference) ||
            (resource.addresses?.length && !resource.addresses.every(ip => machine.ipAddresses.includes(ip)))) {
          throw new Error(`${this.org}: refusing to remove changed machine ${resource.reference}`)
        }
        await recovery.update(resource, { id: String(machine.id) })
        await this.page.goto('/machines')
        const row = this.page.getByRole('row').filter({ has: this.page.getByText(machine.givenName, { exact: true }) })
        await row.getByRole('button', { name: '⋯', exact: true }).click()
        await this.page.getByRole('menuitem', { name: 'Remove', exact: true }).click()
        await confirmDialog(this.page, 'Remove')
        await poll(`${this.org} machine removal`, 15_000, async () =>
          (await this.read<MachinesResponse>('/api/machines')).machines.some(m => String(m.id) === resource.id) ? undefined : true)
      }
    } else {
      if (!resource.id) throw new Error(`${this.org}: indeterminate route creation ${resource.reference}; manual recovery required`)
      const route = (await this.read<FunnelRoutesResponse>('/api/funnel')).funnelRoutes.find(r => String(r.id) === resource.id)
      if (route) {
        if (`${route.label}:${route.targetNodeId}:${route.targetPort}` !== resource.reference) throw new Error(`${this.org}: refusing to remove changed route ID ${resource.id}`)
        await this.page.goto('/funnel')
        const row = this.page.getByRole('row').filter({ has: this.page.getByRole('link', { name: `https://${route.hostname}`, exact: true }) })
        await row.getByRole('button', { name: 'Remove', exact: true }).click()
        await confirmDialog(this.page, 'Remove route')
        await poll(`${this.org} funnel removal`, 15_000, async () =>
          (await this.read<FunnelRoutesResponse>('/api/funnel')).funnelRoutes.some(r => String(r.id) === resource.id) ? undefined : true)
      }
    }
    await recovery.released(resource)
  }
}
