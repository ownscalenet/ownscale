import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { isIP } from 'node:net'
import { bridgeBinary, caFile, enrollmentTimeoutMs, root, scratchDir } from './environment'

export interface NodeInfo {
  node: string
  hostname: string
  addresses: string[]
  backendState: string
}

export interface ServiceInfo {
  node: string
  service: string
  identity: string
  addresses: string[]
  port: number
}

export interface ProbeResult {
  ok: boolean
  statusCode: number
  identity: string
  durationMs: number
  error?: { code: string; message: string }
}

interface StartOptions {
  command?: string
  args?: string[]
  rpcGraceMs?: number
  shutdownGraceMs?: number
  killGraceMs?: number
  helloTimeoutMs?: number
}

const maxFrame = 65_536
const messages: Record<string, string> = {
  invalid_command: 'Invalid command, arguments, framing, or request ID',
  unsupported_version: 'Unsupported protocol version',
  duplicate_handle: 'Handle is already reserved',
  unknown_handle: 'Handle does not exist',
  closed_node: 'Node is closed',
  timeout: 'Operation deadline exceeded',
  unreachable: 'Network endpoint is unreachable',
  refused: 'Network connection was refused',
  cancelled: 'Operation was cancelled',
  identity_mismatch: 'Endpoint returned a different identity',
  invalid_response: 'Endpoint returned an invalid response',
  bridge_failure: 'Bridge operation or cleanup failed',
}
const handle = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(v)
const text = (v: unknown, max = 256): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v)
const integer = (v: unknown, min: number, max: number): v is number =>
  typeof v === 'number' && Number.isSafeInteger(v) && v >= min && v <= max
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)
const keys = (v: Record<string, unknown>, required: string[], optional: string[] = []) =>
  required.every(k => Object.hasOwn(v, k)) && Object.keys(v).every(k => [...required, ...optional].includes(k))
const protocolError = (v: unknown): v is { code: string; message: string } =>
  object(v) && keys(v, ['code', 'message']) && typeof v.code === 'string' &&
  Object.hasOwn(messages, v.code) && text(v.message, 256)
const nodeInfo = (v: unknown): v is NodeInfo => object(v) &&
  keys(v, ['node', 'hostname', 'addresses', 'backendState']) &&
  handle(v.node) && handle(v.hostname) && text(v.backendState, 64) &&
  Array.isArray(v.addresses) && v.addresses.length <= 16 &&
  v.addresses.every(a => typeof a === 'string' && isIP(a) !== 0)
const serviceInfo = (v: unknown): v is ServiceInfo => object(v) &&
  keys(v, ['node', 'service', 'identity', 'addresses', 'port']) &&
  handle(v.node) && handle(v.service) && text(v.identity) && v.identity.length > 0 &&
  integer(v.port, 1, 65535) && Array.isArray(v.addresses) && v.addresses.length > 0 &&
  v.addresses.length <= 16 && v.addresses.every(a => {
    if (typeof a !== 'string') return false
    const match = /^(?:\[([^\]]+)\]|([^:]+)):(\d+)$/.exec(a)
    return !!match && isIP(match[1] ?? match[2]) !== 0 && Number(match[3]) === v.port
  })
const probeResult = (v: unknown): v is ProbeResult => object(v) &&
  keys(v, ['ok', 'statusCode', 'identity', 'durationMs'], ['error']) &&
  typeof v.ok === 'boolean' && integer(v.statusCode, 0, 599) &&
  (v.statusCode === 0 || (v.statusCode as number) >= 100) &&
  text(v.identity) && integer(v.durationMs, 0, 300_000) &&
  (v.ok ? !Object.hasOwn(v, 'error') && v.statusCode === 200 && v.identity.length > 0 : protocolError(v.error))

interface Pending {
  id: string
  op: string
  validate: (value: unknown) => boolean
  resolve: (value: unknown) => void
  reject: (error: Error) => void
  timer: NodeJS.Timeout
}

export class Bridge {
  private readonly child: ChildProcessWithoutNullStreams
  private buffer = Buffer.alloc(0)
  private pending?: Pending
  private sequence = 0
  private queue: Promise<unknown> = Promise.resolve()
  private fault?: Error
  private exited = false
  private exitCode: number | null = null
  private exitSignal: NodeJS.Signals | null = null
  private stopping = false
  private forced = false
  private stderrBytes = 0
  private frames = 0
  private termination?: Promise<void>
  private shutdownPromise?: Promise<void>
  private readonly exit: Promise<void>
  private readonly rpcGraceMs: number
  private readonly shutdownGraceMs: number
  private readonly killGraceMs: number

  private constructor(options: StartOptions) {
    this.rpcGraceMs = options.rpcGraceMs ?? 5000
    this.shutdownGraceMs = options.shutdownGraceMs ?? 10_000
    this.killGraceMs = options.killGraceMs ?? 2000
    this.child = spawn(options.command ?? bridgeBinary, options.args ?? [], {
      cwd: root,
      env: { ...process.env, ...(caFile ? { SSL_CERT_FILE: caFile } : {}), TS_NO_LOGS_NO_SUPPORT: 'true', TMPDIR: scratchDir },
      stdio: ['pipe', 'pipe', 'pipe'],
    })
    this.exit = new Promise(resolve => {
      this.child.once('close', (code, signal) => {
        this.exited = true
        this.exitCode = code
        this.exitSignal = signal
        if (this.buffer.length || this.pending || !this.stopping || code !== 0)
          this.fail('Bridge exited unexpectedly')
        resolve()
      })
    })
    this.child.on('error', () => this.fail('Bridge process failed to start or communicate'))
    this.child.stdin.on('error', () => this.fail('Bridge input stream failed'))
    this.child.stdout.on('error', () => this.fail('Bridge output stream failed'))
    this.child.stderr.on('error', () => this.fail('Bridge diagnostic stream failed'))
    this.child.stderr.on('data', (data: Buffer) => {
      // Keep counts, never diagnostic text: upstream logs may contain credentials.
      this.stderrBytes = Math.min(Number.MAX_SAFE_INTEGER, this.stderrBytes + data.length)
    })
    this.child.stdout.on('data', (data: Buffer) => this.receive(data))
  }

  static async start(options: StartOptions = {}): Promise<Bridge> {
    const bridge = new Bridge(options)
    try {
      await bridge.call('hello', {}, options.helloTimeoutMs ?? 5000, value => object(value) &&
        keys(value, ['ready', 'maxTimeoutMs', 'maxFrameBytes']) && value.ready === true &&
        value.maxTimeoutMs === 300_000 && value.maxFrameBytes === maxFrame)
      return bridge
    } catch (error) {
      await bridge.terminate()
      throw new Error(`${error instanceof Error ? error.message : 'Bridge startup failed'}; ${bridge.diagnostics()}`)
    }
  }

  create(args: { node: string; hostname: string; controlURL: string; authKey: string },
    timeoutMs = enrollmentTimeoutMs): Promise<NodeInfo> {
    return this.call('create', args, timeoutMs, v => nodeInfo(v) && v.node === args.node && v.hostname === args.hostname)
  }

  status(node: string): Promise<NodeInfo> {
    return this.call('status', { node }, 5000, v => nodeInfo(v) && v.node === node)
  }

  serve(node: string, service: string, port: number): Promise<ServiceInfo> {
    return this.call('serve', { node, service, port }, 5000,
      v => serviceInfo(v) && v.node === node && v.service === service && (port === 0 || v.port === port))
  }

  async probe(node: string, url: string, expectedIdentity: string, timeoutMs = 5000): Promise<ProbeResult> {
    const result = await this.call<ProbeResult>('probe', { node, url, expectedIdentity }, timeoutMs,
      v => probeResult(v) && (!v.ok || v.identity === expectedIdentity))
    if (result.error) result.error.message = messages[result.error.code]
    return result
  }

  async close(node: string): Promise<void> {
    await this.call('close', { node }, 10_000,
      v => object(v) && keys(v, ['node', 'closed']) && v.node === node && v.closed === true)
  }

  diagnostics(): string {
    return JSON.stringify({
      pid: this.child.pid ?? null, requests: this.sequence, replies: this.frames,
      stderrBytes: this.stderrBytes, exited: this.exited, exitCode: this.exitCode,
      exitSignal: this.exitSignal, forcedCleanup: this.forced, failed: !!this.fault,
    })
  }

  shutdown(): Promise<void> {
    this.shutdownPromise ??= this.finish()
    return this.shutdownPromise
  }

  private async finish(): Promise<void> {
    this.stopping = true
    let error: unknown
    let timer: NodeJS.Timeout | undefined
    try {
      await Promise.race([
        this.call('shutdown', {}, this.shutdownGraceMs,
          v => object(v) && keys(v, ['closed']) && v.closed === true, true),
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => reject(new Error('Bridge shutdown deadline exceeded')),
            this.shutdownGraceMs + this.rpcGraceMs)
        }),
      ])
      this.child.stdin.end()
      if (!await this.waitExit(this.shutdownGraceMs)) error = new Error('Bridge did not exit after shutdown')
    } catch (caught) { error = caught } finally { clearTimeout(timer) }
    if (!this.exited) await this.terminate()
    if (error || this.forced || this.fault)
      throw new Error(`Bridge cleanup failed; ${this.diagnostics()}`)
  }

  private call<T>(op: string, args: unknown, timeoutMs: number,
    validate: (value: unknown) => boolean, teardown = false): Promise<T> {
    if (!integer(timeoutMs, 1, 300_000)) return Promise.reject(new Error('Invalid bridge operation timeout'))
    if (this.stopping && !teardown) return Promise.reject(new Error('Bridge is shutting down'))
    const next = this.queue.then(() => new Promise<T>((resolve, reject) => {
      if (this.fault || this.exited) {
        reject(this.fault ?? new Error('Bridge is closed'))
        return
      }
      const id = `rpc-${++this.sequence}`
      const frame = JSON.stringify({ version: 1, id, op, timeoutMs, args }) + '\n'
      if (Buffer.byteLength(frame) > maxFrame) {
        reject(new Error('Bridge request exceeds frame limit'))
        return
      }
      const timer = setTimeout(() => this.fail('Bridge RPC deadline exceeded'), timeoutMs + this.rpcGraceMs)
      this.pending = { id, op, validate, resolve: value => resolve(value as T), reject, timer }
      this.child.stdin.write(frame, error => { if (error) this.fail('Bridge input stream failed') })
    }))
    this.queue = next.catch(() => {})
    return next
  }

  private receive(data: Buffer): void {
    if (this.fault) return
    let offset = 0
    while (offset < data.length) {
      const newline = data.indexOf(10, offset)
      const end = newline === -1 ? data.length : newline
      if (this.buffer.length + end - offset > maxFrame) {
        this.fail('Bridge response exceeds frame limit')
        return
      }
      this.buffer = Buffer.concat([this.buffer, data.subarray(offset, end)])
      if (newline === -1) return
      const line = this.buffer
      this.buffer = Buffer.alloc(0)
      offset = newline + 1
      let value: unknown
      try { value = JSON.parse(line.toString('utf8')) } catch {
        this.fail('Bridge returned malformed JSON')
        return
      }
      const pending = this.pending
      if (!pending || !object(value) || value.version !== 1 || value.id !== pending.id ||
        !(keys(value, ['version', 'id', 'result']) || keys(value, ['version', 'id', 'error'])) ||
        (Object.hasOwn(value, 'error') ? !protocolError(value.error) : !pending.validate(value.result))) {
        this.fail('Bridge returned an invalid response shape or request ID')
        return
      }
      clearTimeout(pending.timer)
      this.pending = undefined
      this.frames++
      if (protocolError(value.error)) pending.reject(new Error(`Bridge ${pending.op} operation failed: ${value.error.code} (${messages[value.error.code]})`))
      else pending.resolve(value.result)
    }
  }

  private fail(message: string): void {
    this.fault ??= new Error(message)
    if (this.pending) {
      clearTimeout(this.pending.timer)
      this.pending.reject(this.fault)
      this.pending = undefined
    }
    if (!this.exited) void this.terminate()
  }

  private async waitExit(ms: number): Promise<boolean> {
    if (this.exited) return true
    let timer: NodeJS.Timeout | undefined
    await Promise.race([this.exit, new Promise<void>(resolve => { timer = setTimeout(resolve, ms) })])
    clearTimeout(timer)
    return this.exited
  }

  private terminate(): Promise<void> {
    this.termination ??= (async () => {
      if (this.exited) return
      this.forced = true
      this.fault ??= new Error('Bridge forced cleanup required')
      if (this.pending) {
        clearTimeout(this.pending.timer)
        this.pending.reject(this.fault)
        this.pending = undefined
      }
      this.child.stdin.end()
      // child.kill targets only this owned process, never a process group.
      this.child.kill('SIGTERM')
      if (!await this.waitExit(this.killGraceMs)) {
        this.child.kill('SIGKILL')
        await this.waitExit(this.killGraceMs)
      }
    })()
    return this.termination
  }
}
