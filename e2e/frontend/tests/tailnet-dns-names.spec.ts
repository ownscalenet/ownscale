import { expect, test } from './support/account'
import { registerRealMachine, teardownRealMachine } from './support/realMachine'

// A device is reachable by name as "<name>.<tailnet name>" (the base domain
// on the DNS page). Names are looked up with a real device's own resolver, so
// they show what devices actually receive. Turning Magic DNS off is not
// tested here: tailscaled keeps answering node names from its local resolver
// then; settings-apply.spec.ts checks the flag reaches the device.

test('devices resolve each other as <name>.<tailnet name>', async ({ page, account }) => {
  test.setTimeout(300_000)
  const converge = { timeout: 90_000, intervals: [2_000] }

  const a = await registerRealMachine(page, account.org)
  let b: Awaited<ReturnType<typeof registerRealMachine>> | undefined
  try {
    b = await registerRealMachine(page, account.org)
    const peer = b
    // MagicDNS answers with the peer's IPv4 address.
    const peerV4 = peer.addresses.filter(ip => !ip.includes(':'))
    expect(peerV4).not.toEqual([])

    const tailnetName = ((await (await page.request.get('/api/dns')).json()).dns.baseDomain as string)
    const resolveOnA = (name: string) => a.bridge.resolve(a.hostname, `${name}.${tailnetName}`)

    await test.step('resolve by name', async () => {
      await expect.poll(() => resolveOnA(peer.hostname), converge).toEqual(
        expect.arrayContaining(peerV4),
      )
      expect(await resolveOnA(`absent${Date.now()}`)).toEqual([])
    })

    await test.step('a renamed device answers to its new name only', async () => {
      const { machines } = (await (await page.request.get('/api/machines')).json()) as {
        machines: { id: string; givenName: string }[]
      }
      const id = machines.find(m => m.givenName === peer.hostname)?.id
      expect(id).toBeTruthy()
      const renamed = `${peer.hostname}-renamed`
      const response = await page.request.post(`/api/machines/${id}/rename`, { data: { name: renamed } })
      expect(response.ok()).toBeTruthy()

      await expect.poll(() => resolveOnA(renamed), converge).toEqual(expect.arrayContaining(peerV4))
      await expect.poll(() => resolveOnA(peer.hostname), converge).toEqual([])
    })
  } finally {
    if (b) await teardownRealMachine(b)
    await teardownRealMachine(a)
  }
})
