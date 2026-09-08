import { mkdir, open, readdir, rename, unlink } from 'node:fs/promises'
import path from 'node:path'

export interface OwnedResource {
  token: string
  org: string
  kind: 'key' | 'machine' | 'route'
  reference: string
  id?: string
  addresses?: string[]
}

interface PolicySnapshot {
  org: string
  original: string
}

/** Recovery keeps private, durable ownership records until restoration succeeds. */
export class Recovery {
  readonly policies: PolicySnapshot[] = []
  readonly resources: OwnedResource[] = []
  private writes: Promise<void> = Promise.resolve()
  private constructor(readonly file: string, readonly runID: string) {}

  static async assertClean(directory: string): Promise<void> {
    await mkdir(directory, { recursive: true, mode: 0o700 })
    if ((await readdir(directory)).length) {
      throw new Error(`Unresolved connectivity recovery artifacts in ${directory}; restore the recorded ACLs and remove only recorded resources before retrying. Do not adopt the current policy.`)
    }
  }

  static async create(directory: string, runID: string): Promise<Recovery> {
    await Recovery.assertClean(directory)
    const recovery = new Recovery(path.join(directory, `${runID}.json`), runID)
    const handle = await open(recovery.file, 'wx', 0o600)
    await handle.close()
    await recovery.persist()
    return recovery
  }

  async snapshot(org: string, original: string): Promise<void> {
    if (this.policies.some(p => p.org === org)) throw new Error(`Policy already captured for ${org}`)
    this.policies.push({ org, original })
    await this.persist()
  }

  async restored(org: string): Promise<void> {
    const index = this.policies.findIndex(p => p.org === org)
    if (index >= 0) this.policies.splice(index, 1)
    await this.persist()
  }

  async own(resource: OwnedResource): Promise<void> {
    this.resources.push(resource)
    await this.persist()
  }

  async update(resource: OwnedResource, metadata: Pick<OwnedResource, 'id' | 'addresses'>): Promise<void> {
    Object.assign(resource, metadata)
    await this.persist()
  }

  async released(resource: OwnedResource): Promise<void> {
    const index = this.resources.indexOf(resource)
    if (index >= 0) this.resources.splice(index, 1)
    await this.persist()
  }

  async finish(): Promise<void> {
    if (this.policies.length || this.resources.length) {
      throw new Error(`Owned resources or policies remain; private recovery instructions: ${this.file}`)
    }
    await unlink(this.file)
    await this.syncDirectory()
  }

  private persist(): Promise<void> {
    const write = this.writes.then(() => this.write())
    this.writes = write.catch(() => {})
    return write
  }

  private async write(): Promise<void> {
    const next = `${this.file}.next`
    const handle = await open(next, 'w', 0o600)
    try {
      await handle.writeFile(JSON.stringify({
        version: 1, runID: this.runID, policies: this.policies, resources: this.resources,
      }, null, 2))
      await handle.sync()
    } finally {
      await handle.close()
    }
    await rename(next, this.file)
    await this.syncDirectory()
  }

  private async syncDirectory(): Promise<void> {
    const directory = await open(path.dirname(this.file), 'r')
    try { await directory.sync() } finally { await directory.close() }
  }
}
