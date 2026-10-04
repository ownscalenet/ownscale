import { randomUUID } from 'node:crypto'
import { mkdir, open, readFile, unlink } from 'node:fs/promises'
import { basename, dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import type { FullConfig } from '@playwright/test'

// guardPath is the lock for one suite. Suites use disjoint organizations
// (see tests/support/account.ts), so different suites may run at once, but
// two runs of the same suite would change the same orgs.
export function guardPath(suite: string): string {
  return resolve(dirname(fileURLToPath(import.meta.url)), `../.${suite}-run.lock`)
}

export async function acquireRunGuard(path: string): Promise<() => Promise<void>> {
  const owner = JSON.stringify({ pid: process.pid, token: randomUUID() })
  await mkdir(dirname(path), { recursive: true })
  const occupied = new Error(`Run guard ${path} is occupied or unavailable. Stop competing runs of this suite; inspect the lock and recovery artifacts before manually removing a stale guard.`)
  let file
  try {
    file = await open(path, 'wx', 0o600)
  } catch {
    // A run killed before its teardown leaves its guard behind. Resources
    // it left are caught separately (see Recovery.assertClean).
    const stale = await staleGuard(path)
    if (!stale) throw occupied
    // Re-read so a guard another run just replaced it with survives.
    if (await readFile(path, 'utf8').catch(() => '') !== stale.content) throw occupied
    await unlink(path)
    console.warn(`Removed stale run guard ${path} of exited process ${stale.pid}`)
    try {
      file = await open(path, 'wx', 0o600)
    } catch {
      throw occupied
    }
  }
  try {
    await file.writeFile(owner)
  } finally {
    await file.close()
  }
  let released = false
  return async () => {
    if (released) return
    if (await readFile(path, 'utf8') !== owner)
      throw new Error('Frontend run guard ownership changed; refusing to remove it')
    await unlink(path)
    released = true
  }
}

// staleGuard returns the guard at path if the process that wrote it has
// exited, or undefined if it's alive or the guard can't be read.
async function staleGuard(path: string): Promise<{ pid: number; content: string } | undefined> {
  let content: string
  let pid: unknown
  try {
    content = await readFile(path, 'utf8')
    pid = JSON.parse(content).pid
  } catch {
    return undefined
  }
  if (typeof pid !== 'number') return undefined
  try {
    process.kill(pid, 0)
    return undefined
  } catch (err) {
    // EPERM means the process exists but belongs to someone else.
    return (err as NodeJS.ErrnoException).code === 'ESRCH' ? { pid, content } : undefined
  }
}

// setup locks the suite of the config it runs for, named after the config
// file: playwright.config.ts -> .playwright-run.lock.
export default async function setup(config: FullConfig) {
  return acquireRunGuard(guardPath(basename(config.configFile ?? 'playwright', '.ts').replace(/\.config$/, '')))
}
