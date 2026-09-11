import type { CDPSession, Page } from '@playwright/test'

/** A CDP virtual authenticator plus the session used to drive it. */
export interface VirtualAuthenticator {
  client: CDPSession
  authenticatorId: string
}

// Mirrors CDP's WebAuthn.Credential (see chromedevtools.github.io/devtools-protocol's
// WebAuthn domain). Binary fields (credentialId, privateKey, userHandle,
// largeBlob) are base64-encoded strings over the wire, so this is a plain,
// JSON-serializable shape suitable for persisting to disk and replaying
// into a different virtual authenticator later.
export interface WebAuthnCredential {
  credentialId: string
  isResidentCredential: boolean
  rpId?: string
  privateKey: string
  userHandle?: string
  signCount: number
  largeBlob?: string
  backupEligibility?: boolean
  backupState?: boolean
  userName?: string
  userDisplayName?: string
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

/**
 * exportCredential returns the single resident credential scoped to rpId
 * from a virtual authenticator, so it can be persisted and later replayed
 * into a different authenticator/browser context via importCredential --
 * letting a later test sign back in as the same passkey user instead of
 * registering a brand-new one.
 */
export async function exportCredential(
  { client, authenticatorId }: VirtualAuthenticator, rpId: string,
): Promise<WebAuthnCredential> {
  const { credentials } = await client.send('WebAuthn.getCredentials', { authenticatorId })
  const matches = credentials.filter(credential => credential.rpId === rpId)
  if (matches.length !== 1) {
    throw new Error(`expected exactly one ${rpId} credential on the virtual authenticator, found ${matches.length}`)
  }
  return matches[0]
}

/** importCredential replays a previously-exported credential into a fresh virtual authenticator. */
export async function importCredential(
  { client, authenticatorId }: VirtualAuthenticator, credential: WebAuthnCredential,
): Promise<void> {
  await client.send('WebAuthn.addCredential', { authenticatorId, credential })
}

/**
 * clearCredentials removes all credentials from a virtual authenticator.
 * Used before re-registering a fresh credential on an authenticator that
 * already holds a stale/rejected one (e.g. imported via importCredential
 * but no longer recognized by the server), so exportCredential can find
 * exactly one match afterwards instead of both.
 */
export async function clearCredentials({ client, authenticatorId }: VirtualAuthenticator): Promise<void> {
  await client.send('WebAuthn.clearCredentials', { authenticatorId })
}
