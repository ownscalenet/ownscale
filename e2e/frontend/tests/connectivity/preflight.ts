import { execFile } from 'node:child_process'
import { lookup } from 'node:dns/promises'
import { chmod, lstat, mkdir, readFile, rename } from 'node:fs/promises'
import { request } from 'node:https'
import { promisify } from 'node:util'
import { acquireRunGuard, guardPath } from '../../run-guard'
import { namesOf } from '../support/accounts'
import { baseDomain, baseURL, bridgeBinary, caFile, dexURL, contentDomain, privateDir, root, scratchDir } from './environment'

export async function buildBridge(): Promise<void> {
  await mkdir(scratchDir, { recursive: true, mode: 0o700 })
  try {
    // Built under a per-process name and renamed into place, so suites and
    // workers building at the same time never run a half-written binary.
    const tmp = `${bridgeBinary}.${process.pid}.tmp`
    await promisify(execFile)('go', ['build', '-o', tmp, './e2e/connectivity/cmd/tscl-bridge'], {
      cwd: root, env: { ...process.env, TMPDIR: scratchDir }, timeout: 300_000, maxBuffer: 64 * 1024,
    })
    await rename(tmp, bridgeBinary)
  } catch {
    throw new Error('Connectivity bridge build failed; run go build -o bin/tscl-bridge ./e2e/connectivity/cmd/tscl-bridge for local diagnostics')
  }
}

async function endpoint(url: string, ca: Buffer | undefined): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const parsed = new URL(url)
    if (parsed.protocol !== 'https:' || parsed.username || parsed.password) {
      reject(new Error('Connectivity endpoints must use HTTPS without credentials'))
      return
    }
    const req = request(parsed, { ...(ca ? { ca } : {}), method: 'GET', timeout: 10_000 }, res => {
      res.resume()
      if (res.statusCode && res.statusCode >= 200 && res.statusCode < 400) resolve()
      else reject(new Error('Connectivity endpoint prerequisite failed (unexpected HTTP status)'))
    })
    req.on('timeout', () => req.destroy(new Error('timeout')))
    req.on('error', () => reject(new Error('Connectivity endpoint unavailable or its certificate is untrusted; check make dev-up and E2E_CA_FILE')))
    req.end()
  })
}

export async function checkPrerequisites(): Promise<void> {
  const hosts = [new URL(baseURL).hostname, new URL(dexURL).hostname,
    ...namesOf('connectivity').flatMap(org =>
      [`${org}.${baseDomain}`, `web1-${org}.${contentDomain}`, `web2-${org}.${contentDomain}`])]
  for (const host of hosts) {
    try { await lookup(host) } catch { throw new Error(`Required host does not resolve: ${host}; add it to /etc/hosts or your local DNS (e.g. dnsmasq)`) }
  }
  let ca: Buffer | undefined
  if (caFile) {
    try { ca = await readFile(caFile) } catch {
      throw new Error('Local CA is unreadable; set E2E_CA_FILE to the existing local-ca/root.crt, or E2E_CA_FILE="" to trust the system CA store')
    }
  }
  await Promise.all([endpoint(baseURL, ca), endpoint(`${dexURL.replace(/\/$/, '')}/.well-known/openid-configuration`, ca)])
  await mkdir(privateDir, { recursive: true, mode: 0o700 })
  const stat = await lstat(privateDir)
  if (!stat.isDirectory() || stat.isSymbolicLink())
    throw new Error('Connectivity private artifact path must be a real directory')
  await chmod(privateDir, 0o700)
}

export default async function setup() {
  const release = await acquireRunGuard(guardPath('playwright.connectivity'))
  try {
    await checkPrerequisites()
    await buildBridge()
    process.env.TMPDIR = scratchDir
    return release
  } catch (error) {
    await release()
    throw error
  }
}
