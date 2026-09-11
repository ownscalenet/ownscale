// lib/webauthn.ts: thin wrapper around @simplewebauthn/browser, adapting
// go-webauthn's server response shapes (which wrap the actual WebAuthn
// options in a top-level "publicKey" field, matching the
// PublicKeyCredentialCreationOptions/RequestOptions IDL names) to
// @simplewebauthn/browser's { optionsJSON } call convention.
import {
  startRegistration,
  startAuthentication,
  browserSupportsWebAuthn,
  type RegistrationResponseJSON,
  type AuthenticationResponseJSON,
  type PublicKeyCredentialCreationOptionsJSON,
  type PublicKeyCredentialRequestOptionsJSON,
} from '@simplewebauthn/browser'

export { browserSupportsWebAuthn }

interface CredentialCreationBody {
  publicKey: PublicKeyCredentialCreationOptionsJSON
}

interface CredentialRequestBody {
  publicKey: PublicKeyCredentialRequestOptionsJSON
}

// createPasskey drives navigator.credentials.create() from a go-webauthn
// CredentialCreation response body (as returned by
// /api/passkey(s)/register/begin) and returns the attestation response to
// POST to the matching .../register/finish endpoint.
export function createPasskey(
  body: CredentialCreationBody,
): Promise<RegistrationResponseJSON> {
  return startRegistration({ optionsJSON: body.publicKey })
}

// getPasskey drives navigator.credentials.get() from a go-webauthn
// CredentialAssertion response body (as returned by
// /api/passkey/login/begin) and returns the assertion response to POST to
// /api/passkey/login/finish.
export function getPasskey(
  body: CredentialRequestBody,
): Promise<AuthenticationResponseJSON> {
  return startAuthentication({ optionsJSON: body.publicKey })
}
