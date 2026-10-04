import { randomBytes } from 'node:crypto'
import { lookup } from 'node:dns/promises'
import type { Page } from '@playwright/test'
import { expect } from '@playwright/test'
import { Bridge } from '../connectivity/bridge'
import { buildBridge } from '../connectivity/preflight'
import { baseDomain, enrollmentTimeoutMs } from '../connectivity/environment'

// registerRealMachine spins up one real, ephemeral tsnet-backed device
// (via tscl-bridge, needing no NET_ADMIN/TUN device, unlike `make
// devbox`'s interactive container) and registers it against orgSubdomain
// using a fresh pre-auth key created through the already
// authenticated page's Settings UI.
export interface RealMachine {
  bridge: Bridge
  hostname: string
  addresses: string[]
}

/**
 * ownMemberID returns the signed-in user's ID, which the register-machine
 * and create-key dialogs use to pick the owner.
 */
export async function ownMemberID(page: Page): Promise<string> {
  const session = await page.request.get('/api/session')
  try {
    if (!session.ok()) throw new Error(`GET /api/session: ${session.status()}`)
    const id = ((await session.json()) as { user?: { ID?: string } }).user?.ID
    if (!id) throw new Error('no signed-in user')
    return id
  } finally {
    await session.dispose()
  }
}

export async function registerRealMachine(page: Page, orgSubdomain: string): Promise<RealMachine> {
  try {
    await lookup(`${orgSubdomain}.${baseDomain}`)
  } catch {
    throw new Error(
      `registerRealMachine requires "${orgSubdomain}.${baseDomain}" to resolve (add it to ` +
        '/etc/hosts, same as documented for make e2e-connectivity in e2e/README.md)',
    )
  }
  await buildBridge()
  const bridge = await Bridge.start()

  const hostname = `mlc${randomBytes(4).toString('hex')}`
  // A headscale restart (from a settings save) can abort this navigation
  // with net::ERR_NETWORK_CHANGED. Retry the navigation itself for a while.
  for (let attempt = 1; ; attempt++) {
    try {
      await page.goto('/settings')
      await page.getByRole('button', { name: 'Create pre-auth key' }).click()
      break
    } catch (err) {
      if (attempt >= 5 || !String(err).includes('ERR_NETWORK_CHANGED')) throw err
      await page.waitForTimeout(2_000)
    }
  }
  const createDialog = page.getByRole('dialog')
  const userSelect = createDialog.getByRole('combobox')
  const ownerID = await ownMemberID(page)
  await userSelect.selectOption(ownerID)
  await createDialog.getByLabel('Reusable', { exact: true }).uncheck()
  await createDialog.getByLabel('Ephemeral', { exact: true }).check()
  await createDialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(createDialog.getByText('Pre-auth key created')).toBeVisible({ timeout: 8_000 })
  const authKey = await createDialog.locator('pre code').first().textContent()
  await createDialog.getByRole('button', { name: 'Done' }).click()
  if (!authKey) {
    await bridge.shutdown()
    throw new Error('pre-auth key was not created')
  }

  const info = await bridge.create(
    { node: hostname, hostname, controlURL: `https://${orgSubdomain}.${baseDomain}`, authKey },
    enrollmentTimeoutMs,
  )

  await page.goto('/machines')
  const row = page.getByRole('row').filter({ hasText: hostname })
  await expect(row).toBeVisible({ timeout: enrollmentTimeoutMs })
  await expect(row.getByText('Connected', { exact: true })).toBeVisible({
    timeout: enrollmentTimeoutMs,
  })

  return { bridge, hostname, addresses: info.addresses }
}

export async function teardownRealMachine(machine: RealMachine): Promise<void> {
  await machine.bridge.shutdown()
}
