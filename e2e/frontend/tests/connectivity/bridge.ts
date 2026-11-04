import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { isIP } from 'node:net'
import { JSONRPCClient, JSONRPCErrorException } from 'json-rpc-2.0'
import { bridgeBinary, caFile, enrollmentTimeoutMs, root, scratchDir } from './environment'

export interface NodeInfo {
  node: string
  hostname: string
  addresses: string[]
  backendState: string
  // authURL is the interactive login a node started by login waits on.
  authURL?: string
}

/** The DNS configuration a node received from its control server. */
export interface DNSInfo {
  node: string
  magicDns: boolean
  domains: string[]
  resolvers: string[]
  fallbackResolvers: string[]
  extraRecords: { name: string; type: string; value: string }[]
}

/** The addresses a node's resolver returned for name; empty if it doesn't exist. */
export interface ResolveInfo {
  node: string
  name: string
  addresses: string[]
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

// The single non-reserved JSON-RPC error code the Go bridge uses to carry an
// application-level protocolError (see protocol.go's appErrorCode); the
// actual {code, message} pair travels in the JSON-RPC error's `data` field.
const appErrorCode = 1
const messages: Record<string, string> = {
  invalid_command: 'Invalid command, arguments, framing, or request ID',
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
  keys(v, ['node', 'hostname', 'addresses', 'backendState'], ['authURL']) &&
  handle(v.node) && handle(v.hostname) && text(v.backendState, 64) &&
  (v.authURL === undefined || (text(v.authURL, 2048) && URL.canParse(v.authURL))) &&
  Array.isArray(v.addresses) && v.addresses.length <= 16 &&
  v.addresses.every(a => typeof a === 'string' && isIP(a) !== 0)
const textList = (v: unknown): v is string[] =>
  Array.isArray(v) && v.length <= 64 && v.every(item => text(item, 512))
const dnsInfo = (v: unknown): v is DNSInfo => object(v) &&
  keys(v, ['node', 'magicDns', 'domains', 'resolvers', 'fallbackResolvers', 'extraRecords']) &&
  handle(v.node) && typeof v.magicDns === 'boolean' &&
  textList(v.domains) && textList(v.resolvers) && textList(v.fallbackResolvers) &&
  Array.isArray(v.extraRecords) && v.extraRecords.length <= 256 &&
  v.extraRecords.every(r => object(r) && keys(r, ['name', 'type', 'value']) &&
    text(r.name, 512) && text(r.type, 16) && text(r.value, 512))
const resolveInfo = (v: unknown): v is ResolveInfo => object(v) &&
  keys(v, ['node', 'name', 'addresses']) && handle(v.node) && text(v.name, 253) &&
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

export class Bridge {
  private readonly child: ChildProcessWithoutNullStreams
  private readonly rpc: JSONRPCClient
  private lineBuffer = ''
  private fault?: Error
  private exited = false
  private exitCode: number | null = null
  private exitSignal: NodeJS.Signals | null = null
  private stopping = false
  private forced = false
  private stderrBytes = 0
  private stderrText = ''
  private static readonly maxStderrText = 4096
  private requests = 0
  private replies = 0
  // Number of calls awaiting a reply. Unlike the old hand-rolled protocol,
  // several calls may be in flight at once (they're ID-correlated by the
  // JSON-RPC client, not queued), so if the process exits while any are
  // still outstanding, that's still a bridge failure worth surfacing.
  private inFlight = 0
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
    this.rpc = new JSONRPCClient(request => {
      const frame = JSON.stringify(request) + '\n'
      return new Promise((resolve, reject) => {
        this.child.stdin.write(frame, error => { if (error) reject(error); else resolve() })
      })
    })
    this.exit = new Promise(resolve => {
      this.child.once('close', (code, signal) => {
        this.exited = true
        this.exitCode = code
        this.exitSignal = signal
        if (this.lineBuffer.length || this.inFlight > 0 || !this.stopping || code !== 0)
          this.fail(`Bridge exited unexpectedly${this.stderrText ? `: ${this.stderrText.trim()}` : ''}`)
        resolve()
      })
    })
    this.child.on('error', () => this.fail('Bridge process failed to start or communicate'))
    this.child.stdin.on('error', () => this.fail('Bridge input stream failed'))
    this.child.stdout.on('error', () => this.fail('Bridge output stream failed'))
    this.child.stderr.on('error', () => this.fail('Bridge diagnostic stream failed'))
    this.child.stderr.on('data', (data: Buffer) => {
      this.stderrBytes = Math.min(Number.MAX_SAFE_INTEGER, this.stderrBytes + data.length)
      this.stderrText = (this.stderrText + data.toString('utf8')).slice(-Bridge.maxStderrText)
    })
    this.child.stdout.setEncoding('utf8')
    this.child.stdout.on('data', (data: string) => this.receive(data))
  }

  static async start(options: StartOptions = {}): Promise<Bridge> {
    const bridge = new Bridge(options)
    try {
      await bridge.call('hello', {}, options.helloTimeoutMs ?? 5000, value => object(value) &&
        keys(value, ['ready']) && value.ready === true)
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

  /** login starts a node without an auth key, waiting at its authURL until close. */
  login(args: { node: string; hostname: string; controlURL: string },
    timeoutMs = enrollmentTimeoutMs): Promise<NodeInfo> {
    return this.call('login', args, timeoutMs, v => nodeInfo(v) && v.node === args.node && !!v.authURL)
  }

  status(node: string): Promise<NodeInfo> {
    return this.call('status', { node }, 5000, v => nodeInfo(v) && v.node === node)
  }

  dns(node: string): Promise<DNSInfo> {
    return this.call('dns', { node }, 10_000, v => dnsInfo(v) && v.node === node)
  }

  /** resolve looks name up with node's own resolver (MagicDNS included). */
  async resolve(node: string, name: string): Promise<string[]> {
    const info = await this.call<ResolveInfo>('resolve', { node, name }, 10_000,
      v => resolveInfo(v) && v.node === node && v.name === name)
    return info.addresses
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
      pid: this.child.pid ?? null, requests: this.requests, replies: this.replies,
      stderrBytes: this.stderrBytes, stderrTail: this.stderrText.trim() || undefined,
      exited: this.exited, exitCode: this.exitCode,
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

  // call sends one JSON-RPC request and validates the reply. Requests are
  // ID-correlated by the JSON-RPC client library, so unlike the old hand-rolled
  // protocol there's no need to queue calls one at a time on this side --
  // the Go bridge already serializes its own handler execution
  // (jrpc2.ServerOptions.Concurrency: 1).
  private async call<T>(op: string, args: unknown, timeoutMs: number,
    validate: (value: unknown) => boolean, teardown = false): Promise<T> {
    if (!integer(timeoutMs, 1, 300_000)) throw new Error('Invalid bridge operation timeout')
    if (this.stopping && !teardown) throw new Error('Bridge is shutting down')
    if (this.fault || this.exited) throw this.fault ?? new Error('Bridge is closed')
    this.requests++
    this.inFlight++
    try {
      let result: unknown
      try {
        result = await this.rpc.timeout(timeoutMs + this.rpcGraceMs).request(op, { ...(args as object), timeout: `${timeoutMs}ms` })
      } catch (error) {
        if (error instanceof JSONRPCErrorException && error.code === appErrorCode && protocolError(error.data)) {
          const pe = error.data
          throw new Error(`Bridge ${op} operation failed: ${pe.code} (${messages[pe.code]})`)
        }
        this.fail('Bridge RPC deadline exceeded or bridge returned an invalid response')
        throw this.fault
      }
      if (!validate(result)) {
        this.fail('Bridge returned an invalid response shape')
        throw this.fault
      }
      this.replies++
      return result as T
    } finally {
      this.inFlight--
    }
  }

  private receive(data: string): void {
    if (this.fault) return
    this.lineBuffer += data
    let newline: number
    while ((newline = this.lineBuffer.indexOf('\n')) >= 0) {
      const line = this.lineBuffer.slice(0, newline)
      this.lineBuffer = this.lineBuffer.slice(newline + 1)
      let value: unknown
      try { value = JSON.parse(line) } catch {
        this.fail('Bridge returned malformed JSON')
        return
      }
      if (!object(value) || value.jsonrpc !== '2.0' || !('id' in value) ||
        !(keys(value, ['jsonrpc', 'id', 'result']) || keys(value, ['jsonrpc', 'id', 'error']))) {
        this.fail('Bridge returned an invalid response shape or request ID')
        return
      }
      this.rpc.receive(value as unknown as Parameters<JSONRPCClient['receive']>[0])
    }
  }

  private fail(message: string): void {
    this.fault ??= new Error(message)
    this.rpc.rejectAllPendingRequests(this.fault.message)
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
      this.rpc.rejectAllPendingRequests(this.fault.message)
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
