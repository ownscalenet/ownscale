// stack.ts: helpers for resetting and starting the real docker-compose
// stack (ownscale + dex + caddy) that these e2e tests run against, plus
// polling for it to be ready. Intentionally shells out to `docker compose`
// and `go run ./cmd/osa` rather than mocking anything -- see plan.md /
// e2e/README.md for why.
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { existsSync } from 'node:fs'
import path from 'node:path'
import https from 'node:https'

const execFileAsync = promisify(execFile)

// Repo root, two levels up from e2e/lib/.
export const REPO_ROOT = path.resolve(import.meta.dirname, '..', '..')

async function run(command: string, args: string[], cwd = REPO_ROOT): Promise<void> {
  try {
    await execFileAsync(command, args, { cwd, maxBuffer: 32 * 1024 * 1024 })
  } catch (err) {
    const e = err as { stdout?: string; stderr?: string; message: string }
    throw new Error(`${command} ${args.join(' ')} failed: ${e.stderr || e.stdout || e.message}`)
  }
}

// removeOrgContainers stops/removes every container ownscale labels as
// belonging to an organization (mirrors the Makefile's `rm-orphans`
// target), so a rerun doesn't collide with a previous run's org
// containers/networks.
async function removeOrgContainers(): Promise<void> {
  const { stdout } = await execFileAsync('docker', [
    'ps', '-aq', '--filter', 'label=ownscale.organization=true',
  ])
  const ids = stdout.split('\n').map((s) => s.trim()).filter(Boolean)
  if (ids.length === 0) return
  await execFileAsync('docker', ['rm', '-f', ...ids])
}

// resetDatabase deletes the SQLite database file (plus WAL/SHM sidecars)
// under data/, so every run starts from an empty, then-migrated schema.
// Safe/expected here: this suite is explicitly allowed to reset local dev
// data (see plan.md). The files are owned by root (created by the
// containerized server, which runs as root against the bind-mounted
// data/ dir), so removing them needs a privilege escalation; `sudo` is
// used rather than Node's fs so this works the same whether or not the
// current user happens to own the files.
async function resetDatabase(): Promise<void> {
  const dataDir = path.join(REPO_ROOT, 'data')
  const files = ['', '-wal', '-shm']
    .map((suffix) => path.join(dataDir, `ownscale.sqlite${suffix}`))
    .filter(existsSync)
  if (files.length === 0) return
  await run('sudo', ['rm', '-f', ...files])
}

// resetAndStartStack tears down any leftover per-organization containers,
// resets the ownscale SQLite database, brings up the core docker-compose
// services, migrates the fresh database, and waits for the app to answer
// requests. Idempotent: safe to call at the start of every test run.
export async function resetAndStartStack(): Promise<void> {
  await removeOrgContainers()
  // Stop only "ownscale" before touching the database file (so nothing has
  // it open -- SQLite on a bind mount can otherwise keep a stale WAL).
  // Deliberately leave dex/caddy running (or bring them up without forcing
  // a restart): caddy has no persistent volume for its on-demand TLS
  // certificate cache (see docker-compose.yml), so recreating it on every
  // run forces it to re-request certs for dex./the org subdomain from
  // scratch every time, which is slow and occasionally flaky (Docker's
  // embedded DNS resolver intermittently failing "ownscale" lookups right
  // after a restart). Reusing the already-running container across runs
  // keeps its cert cache warm and avoids that flakiness entirely.
  await run('docker', ['compose', 'stop', 'ownscale', 'litestream'])
  await resetDatabase()
  await run('docker', ['compose', 'up', '-d', '--build', 'local-ca-init', 'dex', 'caddy'])
  // Run migrations inside a throwaway container using the same image as
  // the "ownscale" service (which runs as root and mounts the same data/
  // dir), rather than `go run` on the host: the data/ directory itself is
  // root-owned (created by the containerized server), so a host-side Go
  // process can't create a new database file there.
  await run('docker', [
    'compose', 'run', '--rm', '--entrypoint', '/app/osa', 'ownscale',
    '--config', '/etc/ownscale/config.yaml', 'migrate', 'up',
  ])
  await run('docker', ['compose', 'up', '-d', 'ownscale'])
  await waitForReady()
}

// waitForReady polls the app's session endpoint (through Caddy, over real
// TLS) until it responds with something other than a connection/502
// error, or throws after timeoutMs.
export async function waitForReady(timeoutMs = 60_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  let lastErr: unknown
  while (Date.now() < deadline) {
    try {
      const status = await getStatus('https://ownscale.test/api/session')
      if (status < 500) return
      lastErr = new Error(`status ${status}`)
    } catch (err) {
      lastErr = err
    }
    await new Promise((r) => setTimeout(r, 1000))
  }
  throw new Error(`stack not ready after ${timeoutMs}ms: ${String(lastErr)}`)
}

function getStatus(url: string): Promise<number> {
  return new Promise((resolve, reject) => {
    const req = https.get(url, { rejectUnauthorized: false, timeout: 5000 }, (res) => {
      res.resume() // drain, we only care about the status code
      resolve(res.statusCode ?? 0)
    })
    req.on('error', reject)
    req.on('timeout', () => req.destroy(new Error('request timed out')))
  })
}
