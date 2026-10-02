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
  let file
  try {
    file = await open(path, 'wx', 0o600)
  } catch {
    throw new Error(`Run guard ${path} is occupied or unavailable. Stop competing runs of this suite; inspect the lock and recovery artifacts before manually removing a stale guard.`)
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

// setup locks the suite of the config it runs for, named after the config
// file: playwright.config.ts -> .playwright-run.lock.
export default async function setup(config: FullConfig) {
  return acquireRunGuard(guardPath(basename(config.configFile ?? 'playwright', '.ts').replace(/\.config$/, '')))
}
