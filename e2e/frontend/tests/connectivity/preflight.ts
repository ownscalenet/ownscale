import { execFile } from 'node:child_process'
import { lookup, Resolver } from 'node:dns/promises'
import { chmod, lstat, mkdir, readFile, rename } from 'node:fs/promises'
import { request } from 'node:https'
import { promisify } from 'node:util'
import { acquireRunGuard, guardPath } from '../../run-guard'
import { baseDomain, baseURL, bridgeBinary, caFile, dexURL, privateDir, root, scratchDir } from './environment'

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

// Used to check whether a host resolves via real public DNS, independent
// of the local resolver's own configuration.
const publicResolver = new Resolver()
publicResolver.setServers(['1.1.1.1', '8.8.8.8'])

async function resolvesViaPublicDNS(host: string): Promise<boolean> {
  try { await publicResolver.resolve4(host); return true } catch {}
  try { await publicResolver.resolve6(host); return true } catch {}
  return false
}

export async function checkPrerequisites(): Promise<void> {
  const hosts = [new URL(baseURL).hostname, new URL(dexURL).hostname,
    ...['aaaa', 'bbbb', 'cccc'].flatMap(org =>
      [`${org}.${baseDomain}`, `web1-${org}.${baseDomain}`, `web2-${org}.${baseDomain}`])]
  const entries = (await readFile('/etc/hosts', 'utf8')).split('\n')
    .flatMap(line => line.split('#')[0].trim().split(/\s+/).slice(1))
  for (const host of hosts) {
    if (await resolvesViaPublicDNS(host)) continue
    if (!entries.includes(host))
      throw new Error(`Missing required /etc/hosts entry: ${host}; configure it manually before running`)
    try { await lookup(host) } catch { throw new Error(`Required host does not resolve: ${host}`) }
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
