import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

export const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..')
export const baseDomain = process.env.E2E_BASE_DOMAIN ?? 'ownscale.test'
// Funnels are served under OWNSCALE_FUNNEL_DOMAIN, which defaults to the base domain.
export const funnelDomain = process.env.E2E_FUNNEL_DOMAIN ?? baseDomain
// The console (and its WebAuthn Relying Party ID) is bound to the "console" subdomain, not the
// bare base domain.
export const consoleDomain = `console.${baseDomain}`
export const baseURL = process.env.E2E_BASE_URL ?? `https://${consoleDomain}`
export const privateDir = resolve(root, 'e2e/.connectivity-private')
export const caFile = process.env.E2E_CA_FILE === ''
  ? undefined
  : resolve(root, process.env.E2E_CA_FILE ??
      `${process.env.OWNSCALE_DATA_DIR ?? 'data'}/local-ca/root.crt`)
export const bridgeBinary = resolve(root, 'bin/tscl-bridge')
export const scratchDir = resolve(root, 'bin/connectivity-work')
export const dexURL = process.env.E2E_DEX_URL ?? `https://dex.${baseDomain}`

function boundedTimeout(name: string, fallback: number): number {
  const value = Number(process.env[name] ?? fallback)
  if (!Number.isInteger(value) || value < 1000 || value > 300_000)
    throw new Error(`${name} must be an integer between 1000 and 300000 milliseconds`)
  return value
}

export const enrollmentTimeoutMs = boundedTimeout('E2E_ENROLLMENT_TIMEOUT_MS', 90_000)
