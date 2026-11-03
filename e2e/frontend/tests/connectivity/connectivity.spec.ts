import type { ProbeResult, ServiceInfo } from './bridge'
import { test, expect, type Endpoint, type Harness } from './fixtures'
import { localTLS } from './environment'
import { poll } from './ui'

const allowPolicy = JSON.stringify({ acls: [{ action: 'accept', src: ['*'], dst: ['*:*'] }] }, null, 2)
const transportErrors = new Set(['timeout', 'unreachable', 'refused'])

function address(service: ServiceInfo): string {
  const endpoint = service.addresses.find(value => /^\d+\.\d+\.\d+\.\d+:\d+$/.test(value))
  if (!endpoint) throw new Error('Service has no IPv4 endpoint; IPv4 is required for this matrix')
  return `http://${endpoint}/`
}

async function observe(harness: Harness, source: Endpoint, service: ServiceInfo, phase: string): Promise<ProbeResult> {
  const result = await harness.bridge.probe(source.info.node, address(service), service.identity, 5000)
  harness.observations.push({
    phase, source: source.info.hostname, destination: service.node,
    ok: result.ok, statusCode: result.statusCode, identity: result.identity,
    durationMs: result.durationMs, errorCode: result.error?.code,
  })
  return result
}

function validTransportFailure(result: ProbeResult): boolean {
  return !result.ok && result.statusCode === 0 && !result.identity && transportErrors.has(result.error?.code ?? '')
}

async function positive(harness: Harness, source: Endpoint, target: ServiceInfo, phase: string, timeout = 90_000): Promise<void> {
  await poll(phase, timeout, async () => {
    const result = await observe(harness, source, target, phase)
    if (result.ok && result.statusCode === 200 && result.identity === target.identity) return true
    if (!validTransportFailure(result)) throw new Error(`${phase}: unexpected result ${result.error?.code ?? 'invalid success'}`)
    return undefined
  })
}

async function healthyPair(harness: Harness, pair: Endpoint[], phase: string): Promise<void> {
  for (const endpoint of pair) {
    const status = await harness.bridge.status(endpoint.info.node)
    if (status.backendState !== 'Running') throw new Error(`${phase}: ${endpoint.info.hostname} is not Running`)
  }
  await positive(harness, pair[0], pair[1].service, `${phase} forward`)
  await positive(harness, pair[1], pair[0].service, `${phase} reverse`)
}

test('real tailnets: bidirectional connectivity, ordered isolation, ACL transitions and public funnel', async ({ harness, browser }) => {
  const { orgs, recovery } = harness
  const pairs: Endpoint[][] = []

  await test.step('capture original policies durably before applying deterministic allow rules', async () => {
    // Capture every org before the first write, so no unrecorded mutation can occur.
    for (const ui of orgs) await recovery.snapshot(ui.org, await ui.policy())
    for (const ui of orgs) await ui.savePolicy(allowPolicy)
  })

  await test.step('enroll six UI-keyed nodes and prove same-org connectivity in both directions', async () => {
    for (const ui of orgs) {
      const pair = [await harness.enroll(ui, 0), await harness.enroll(ui, 1)]
      pairs.push(pair)
      await healthyPair(harness, pair, `${ui.org} same-org`)
    }
  })

  await test.step('all six ordered cross-org pairs remain isolated, with healthy controls and collision validation', async () => {
    for (let sourceOrg = 0; sourceOrg < pairs.length; sourceOrg++) {
      for (let targetOrg = 0; targetOrg < pairs.length; targetOrg++) {
        if (sourceOrg === targetOrg) continue
        const sourcePair = pairs[sourceOrg]
        const targetPair = pairs[targetOrg]
        const phase = `${orgs[sourceOrg].org}->${orgs[targetOrg].org}`
        await healthyPair(harness, sourcePair, `${phase} source before`)
        await healthyPair(harness, targetPair, `${phase} target before`)
        for (const source of sourcePair) {
          for (const target of targetPair) {
            const result = await observe(harness, source, target.service, `${phase} isolation`)
            if (result.ok || result.identity === target.service.identity) throw new Error(`${phase}: remote org identity is reachable`)
            if (result.error?.code === 'identity_mismatch' && result.statusCode === 200) {
              const local = sourcePair.find(candidate =>
                candidate.service.identity === result.identity &&
                address(candidate.service) === address(target.service))
              if (!local) throw new Error(`${phase}: unexpected identity is not an overlapping endpoint owned by the source org`)
              await positive(harness, source, local.service, `${phase} overlap-local identity`, 15_000)
              harness.observations.push({ phase: 'validated-address-collision', sourceOrg: source.ui.org, targetOrg: target.ui.org, localNode: local.info.hostname })
            } else if (!validTransportFailure(result)) {
              throw new Error(`${phase}: ${result.error?.code ?? 'invalid response'} is not proof of isolation`)
            }
          }
        }
        await healthyPair(harness, sourcePair, `${phase} source after`)
        await healthyPair(harness, targetPair, `${phase} target after`)
      }
    }
  })

  await test.step('ACL deny converges while control/reverse/service liveness stay healthy, then re-allow', async () => {
    const [source, target] = pairs[0]
    const control = await harness.bridge.serve(target.info.node, `${target.info.node}control`, 18081)
    await positive(harness, source, target.service, 'ACL initial test port')
    await positive(harness, source, control, 'ACL initial control port')
    const sourceIP = source.info.addresses.find(ip => !ip.includes(':'))!
    const targetIP = target.info.addresses.find(ip => !ip.includes(':'))!
    const denyPolicy = JSON.stringify({
      acls: [
        { action: 'accept', src: ['*'], dst: [`*:${control.port}`] },
        { action: 'accept', src: [targetIP], dst: [`${sourceIP}:${source.service.port}`, `${targetIP}:${target.service.port}`] },
      ],
    }, null, 2)
    await orgs[0].savePolicy(denyPolicy)
    let denied = 0
    await poll('ACL data-plane deny convergence (three consecutive denials)', 120_000, async () => {
      await positive(harness, source, control, 'ACL control before denial', 15_000)
      await positive(harness, target, source.service, 'ACL reverse control', 15_000)
      await positive(harness, target, target.service, 'ACL target service remains alive', 15_000)
      const result = await observe(harness, source, target.service, 'ACL denied port')
      if (result.ok) {
        denied = 0
        return undefined
      }
      if (!validTransportFailure(result)) throw new Error(`ACL: ${result.error?.code ?? 'invalid response'} is not a denial`)
      await positive(harness, source, control, 'ACL control after denial', 15_000)
      return ++denied >= 3 ? true : undefined
    })
    await orgs[0].savePolicy(allowPolicy)
    await positive(harness, source, target.service, 'ACL re-allowed test port')
    await positive(harness, source, control, 'ACL re-allowed control port')
  })

  await test.step('public unauthenticated funnel serves the exact identity and removal gives a missing route', async () => {
    const target = pairs[0][1]
    if (!target.resource.id) throw new Error('Real machine ID is required for funnel target')
    const route = await orgs[0].createFunnel(target.resource.id, target.service.port, recovery)
    const publicContext = await browser.newContext({ ignoreHTTPSErrors: localTLS })
    try {
      expect(await publicContext.cookies()).toEqual([])
      const publicURL = `https://${route.hostname}/`

      // The funnel's shared tsnet server is started eagerly by
      // CreateFunnelRoute (before it even returns), so it's already up
      // and serving by the time we get here.
      {
        const response = await publicContext.request.get(publicURL, { timeout: 10_000, maxRedirects: 0 })
        try {
          if (response.status() !== 200) throw new Error(`Public funnel returned HTTP ${response.status()}, not an identity endpoint`)
          const data = await response.json().catch(() => null)
          if (data?.identity !== target.service.identity) throw new Error('Public funnel returned the wrong or malformed identity')
          harness.observations.push({ phase: 'public-funnel', statusCode: 200, identity: data.identity })
        } finally { await response.dispose() }
      }
      const ownedRoute = recovery.resources.find(r => r.kind === 'route' && r.id === String(route.id))!
      await orgs[0].removeResource(ownedRoute, recovery)
      {
        const response = await publicContext.request.get(publicURL, { timeout: 10_000, maxRedirects: 0 })
        try {
          const body = await response.text()
          if (response.status() !== 404 || body.trim() !== '404 page not found') {
            throw new Error(`Removed funnel must return the missing-route HTTP 404, got HTTP ${response.status()}`)
          }
          harness.observations.push({ phase: 'public-funnel-removed', statusCode: response.status() })
        } finally { await response.dispose() }
      }
    } finally { await publicContext.close() }
  })
})
