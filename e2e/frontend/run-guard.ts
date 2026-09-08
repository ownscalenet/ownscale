import { randomUUID } from 'node:crypto'
import { mkdir, open, readFile, unlink } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const guardPath = resolve(dirname(fileURLToPath(import.meta.url)), '../.frontend-run.lock')

export async function acquireRunGuard(path = guardPath): Promise<() => Promise<void>> {
  const owner = JSON.stringify({ pid: process.pid, token: randomUUID() })
  await mkdir(dirname(path), { recursive: true })
  let file
  try {
    file = await open(path, 'wx', 0o600)
  } catch {
    throw new Error('Frontend run guard is occupied or unavailable. Stop competing runs; inspect e2e/.frontend-run.lock and recovery artifacts before manually removing a stale guard.')
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

export default async function setup() {
  return acquireRunGuard()
}
