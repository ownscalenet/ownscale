import { expect, test } from '../support/account'
import { extraTestEnabled } from '../support/extraTests'
import { getPolicy, putPolicy } from '../support/policy'
import { registerRealMachine, teardownRealMachine, type RealMachine } from '../support/realMachine'

const allowAll = `{"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}]}`

// Each run issues a Let's Encrypt certificate for a new hostname, using one of the
// org's weekly certificates, so it only runs where EXTRA_TESTS asks for it.
test('a machine gets a trusted HTTPS certificate for its MagicDNS name', async ({ page, account }) => {
  test.skip(!extraTestEnabled('serve'), 'set EXTRA_TESTS=<base domain>:serve to run')
  test.setTimeout(8 * 60_000)

  const original = await getPolicy(page)
  const machines: RealMachine[] = []
  try {
    await putPolicy(page, allowAll)
    const server = await registerRealMachine(page, account.org)
    machines.push(server)
    const client = await registerRealMachine(page, account.org)
    machines.push(client)

    const service = await server.bridge.serveTLS(server.hostname, 'https')
    expect(service.domain).toMatch(new RegExp(`^${server.hostname}\\.`))

    const ip = server.addresses.find(a => !a.includes(':'))
    expect(ip, 'server has an IPv4 tailnet address').toBeTruthy()
    // The probe verifies the certificate against the system roots for the server's name.
    await expect.poll(async () => {
      const r = await client.bridge.probe(client.hostname, `https://${ip}`, service.identity, 15_000, service.domain)
      return r.ok ? 'ok' : `${r.error?.code}: ${r.error?.message}`
    }, { timeout: 60_000, intervals: [2_000] }).toBe('ok')
  } finally {
    for (const m of machines.reverse()) await teardownRealMachine(m)
    await putPolicy(page, original)
  }
})
