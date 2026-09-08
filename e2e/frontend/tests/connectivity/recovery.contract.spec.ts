import { readFile, stat, rm, writeFile } from 'node:fs/promises'
import { test, expect } from '@playwright/test'
import { Recovery, type OwnedResource } from './recovery'

test('private snapshots and owned IDs survive until all restoration succeeds', async ({}, testInfo) => {
  const directory = testInfo.outputPath('recovery')
  try {
    const recovery = await Recovery.create(directory, 'contract')
    await recovery.snapshot('aaaa', '{"acls": []}\n')
    const resource: OwnedResource = { token: 'owned', org: 'aaaa', kind: 'machine', reference: 'ctcontractaaaa0' }
    await recovery.own(resource)
    await recovery.update(resource, { id: 'real-machine-id', addresses: ['100.64.0.1'] })
    expect((await stat(directory)).mode & 0o777).toBe(0o700)
    expect((await stat(recovery.file)).mode & 0o777).toBe(0o600)
    const saved = JSON.parse(await readFile(recovery.file, 'utf8'))
    expect(saved.policies).toEqual([{ org: 'aaaa', original: '{"acls": []}\n' }])
    expect(saved.resources[0]).toEqual(resource)
    await expect(Recovery.assertClean(directory)).rejects.toThrow('Unresolved connectivity recovery')
    await expect(recovery.finish()).rejects.toThrow('Owned resources or policies remain')
    await recovery.released(resource)
    await expect(recovery.finish()).rejects.toThrow('Owned resources or policies remain')
    await recovery.restored('aaaa')
    await recovery.finish()
    await Recovery.assertClean(directory)
  } finally { await rm(directory, { recursive: true, force: true }) }
})

test('a failed restore leaves the original private snapshot and blocks adoption', async ({}, testInfo) => {
  const directory = testInfo.outputPath('recovery')
  try {
    const recovery = await Recovery.create(directory, 'failedrestore')
    await recovery.snapshot('bbbb', '// private original\n{}')
    await expect(recovery.snapshot('bbbb', '{}')).rejects.toThrow('already captured')
    await expect(Recovery.create(directory, 'next')).rejects.toThrow('Do not adopt')
    expect(JSON.parse(await readFile(recovery.file, 'utf8')).policies[0].original).toBe('// private original\n{}')
  } finally { await rm(directory, { recursive: true, force: true }) }
})

test('an interrupted journal write is also a blocker', async ({}, testInfo) => {
  const directory = testInfo.outputPath('recovery')
  try {
    await Recovery.assertClean(directory)
    await writeFile(`${directory}/interrupted.json.next`, '', { mode: 0o600 })
    await expect(Recovery.assertClean(directory)).rejects.toThrow('Unresolved connectivity recovery')
  } finally { await rm(directory, { recursive: true, force: true }) }
})

test('parallel org cleanup serializes durable journal writes', async ({}, testInfo) => {
  const directory = testInfo.outputPath('recovery')
  try {
    const recovery = await Recovery.create(directory, 'parallel')
    const resources: OwnedResource[] = ['aaaa', 'bbbb', 'cccc'].map(org => ({
      org, kind: 'machine', token: org, reference: `ct${org}`,
    }))
    await Promise.all(resources.map(resource => recovery.own(resource)))
    expect(JSON.parse(await readFile(recovery.file, 'utf8')).resources).toHaveLength(3)
    await Promise.all(resources.map(resource => recovery.released(resource)))
    expect(JSON.parse(await readFile(recovery.file, 'utf8')).resources).toEqual([])
    await recovery.finish()
  } finally { await rm(directory, { recursive: true, force: true }) }
})
