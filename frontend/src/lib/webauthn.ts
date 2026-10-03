// lib/webauthn.ts adapts go-webauthn's responses, which wrap the WebAuthn
// options in "publicKey", to @simplewebauthn/browser's { optionsJSON }.
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

// createPasskey runs navigator.credentials.create() with a register/begin
// response and returns what register/finish expects.
export function createPasskey(body: CredentialCreationBody): Promise<RegistrationResponseJSON> {
  return startRegistration({ optionsJSON: body.publicKey })
}

// getPasskey runs navigator.credentials.get() with a login/begin response
// and returns what login/finish expects.
export function getPasskey(body: CredentialRequestBody): Promise<AuthenticationResponseJSON> {
  return startAuthentication({ optionsJSON: body.publicKey })
}
