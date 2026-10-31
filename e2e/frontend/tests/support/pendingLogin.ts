import { randomBytes } from 'node:crypto'
import { lookup } from 'node:dns/promises'
import { Bridge } from '../connectivity/bridge'
import { buildBridge } from '../connectivity/preflight'
import { baseDomain } from '../connectivity/environment'

export interface PendingLogin {
  // authURL returns headscale's current /register/<auth ID> page for the
  // waiting machine. A headscale restart forgets the auth ID and the machine
  // gets a new one, so read it again for each visit.
  authURL(): Promise<string>
  close(): Promise<void>
}

// startPendingLogin starts a machine that waits for an interactive login to
// org's tailnet, like `tailscale up` without an auth key. Headscale only
// starts an OIDC login for an auth ID a machine is waiting on, and leaves the
// registration pending until someone confirms it, so the URL can be visited
// any number of times.
export async function startPendingLogin(org: string): Promise<PendingLogin> {
  try {
    await lookup(`${org}.${baseDomain}`)
  } catch {
    throw new Error(
      `startPendingLogin requires "${org}.${baseDomain}" to resolve (add it to ` +
        '/etc/hosts, same as documented for make e2e-connectivity in e2e/README.md)',
    )
  }
  await buildBridge()
  const bridge = await Bridge.start()
  const node = `pl${randomBytes(4).toString('hex')}`
  try {
    await bridge.login({ node, hostname: node, controlURL: `https://${org}.${baseDomain}` })
  } catch (err) {
    await bridge.shutdown().catch(() => {})
    throw err
  }
  return {
    authURL: async () => {
      const { authURL } = await bridge.status(node)
      if (!authURL) throw new Error(`${node} is not waiting for a login`)
      return authURL
    },
    close: () => bridge.shutdown(),
  }
}
