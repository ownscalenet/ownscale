import { test, expect } from '@playwright/test'
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { randomUUID } from 'node:crypto'
import { resolve } from 'node:path'
import { acquireRunGuard } from '../../run-guard'
import { scratchDir } from './environment'

test('exclusive run guard releases only its own token and never adopts an existing guard', async () => {
  const dir = resolve(scratchDir, `guard-${randomUUID()}`)
  const path = resolve(dir, 'run.lock')
  await mkdir(dir, { recursive: true })
  try {
    const release = await acquireRunGuard(path)
    const original = await readFile(path, 'utf8')
    await expect(acquireRunGuard(path)).rejects.toThrow('occupied')
    expect(await readFile(path, 'utf8')).toBe(original)
    await writeFile(path, '{"pid":0,"token":"someone-else"}')
    await expect(release()).rejects.toThrow('ownership changed')
    expect(await readFile(path, 'utf8')).toContain('someone-else')
    await writeFile(path, original)
    await release()
    await release()
    const nextRelease = await acquireRunGuard(path)
    await nextRelease()
  } finally { await rm(dir, { recursive: true, force: true }) }
})
