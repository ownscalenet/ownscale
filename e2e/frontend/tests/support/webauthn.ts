import type { CDPSession, Page } from '@playwright/test'

/** A CDP virtual authenticator plus the session used to drive it. */
export interface VirtualAuthenticator {
  client: CDPSession
  authenticatorId: string
}

// addVirtualAuthenticator wires up Chrome DevTools Protocol's WebAuthn
// domain so the browser answers navigator.credentials.create()/.get()
// itself, without a real security key or platform authenticator. The
// authenticator is configured as a resident-key-capable, pre-verified
// "platform" authenticator so both the register (createPasskey) and the
// discoverable/usernameless login (getPasskey) ceremonies succeed without
// any UI prompt.
export async function addVirtualAuthenticator(page: Page): Promise<VirtualAuthenticator> {
  const client = await page.context().newCDPSession(page)
  await client.send('WebAuthn.enable')
  const { authenticatorId } = await client.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  })
  return { client, authenticatorId }
}
