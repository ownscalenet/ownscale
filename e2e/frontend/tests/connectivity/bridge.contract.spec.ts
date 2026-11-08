import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { Bridge } from './bridge'
import { bridgeBinary, caFile, root, scratchDir } from './environment'

const secret = 'contract-private-key-cookie-password'
// A JSON-RPC mock bridge process: replies mirror the real Go bridge's
// wire shape (see internal/bridge/protocol.go's toRPCError), but each
// "mode" deliberately misbehaves in one way to exercise the client's
// defenses (malformed JSON, wrong reply id, missing/invalid fields, etc).
const mock = `
const readline = require('node:readline');
const mode = process.argv[1];
if (mode === 'stubborn') process.on('SIGTERM', () => {});
readline.createInterface({ input: process.stdin }).on('line', line => {
  const q = JSON.parse(line);
  const reply = result => JSON.stringify({ jsonrpc: '2.0', id: q.id, result }) + '\\n';
  if (q.method === 'hello') {
    if (mode === 'hello-timeout') return;
    if (mode === 'hello-exit') { process.exit(0); return; }
    if (mode === 'hello-malformed') { process.stdout.write('${secret}\\n'); return; }
    process.stdout.write(reply({ ready: true }));
    return;
  }
  if (q.method === 'shutdown') {
    if (mode === 'stubborn') { setInterval(() => {}, 1000); return; }
    process.stdout.write(reply({ closed: true }), () => process.exit(0));
    return;
  }
  if (mode === 'exit') { process.stdout.write('{"jsonrpc":'); process.exit(7); }
  if (mode === 'timeout') return;
  if (mode === 'malformed') { process.stdout.write('PRIVATE-NON-JSON\\n'); return; }
  if (mode === 'wrong-id') { process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:'wrong',result:{}})+'\\n'); return; }
  if (mode === 'wrong-jsonrpc') { process.stdout.write(JSON.stringify({jsonrpc:'1.0',id:q.id,result:{}})+'\\n'); return; }
  if (mode === 'wrong-shape') { process.stdout.write(reply({ok:true})); return; }
  if (mode === 'both') { process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:q.id,result:{},error:{code:1,message:'x',data:{code:'timeout',message:'SECRET'}}})+'\\n'); return; }
  if (mode === 'error') { process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:q.id,error:{code:1,message:'${secret}',data:{code:'invalid_command',message:'${secret}'}}})+'\\n'); return; }
  if (mode === 'unknown-error') { process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:q.id,error:{code:1,message:'${secret}',data:{code:'${secret}',message:'${secret}'}}})+'\\n'); return; }
  if (mode === 'tls') {
    if (q.method === 'serveTLS') { process.stdout.write(reply({node:q.params.node,service:q.params.service,identity:'id',addresses:['100.64.0.1:443'],port:443,domain:'host.org.example.com'})); return; }
    if (q.method === 'probe') { process.stdout.write(reply(q.params.serverName === 'host.org.example.com' ? {ok:true,statusCode:200,identity:'id',durationMs:1} : {ok:false,statusCode:0,identity:'',durationMs:1,error:{code:'timeout',message:'x'}})); return; }
  }
  process.stderr.write('${secret}'.repeat(5000));
  if (q.method === 'create') {
    if (q.params.authKey !== '${secret}' || process.argv.includes(q.params.authKey) ||
        process.env.SSL_CERT_FILE !== ${caFile ? JSON.stringify(caFile) : 'undefined'} || process.env.TS_NO_LOGS_NO_SUPPORT !== 'true')
      process.exit(9);
    const createFrame = reply({node:q.params.node,hostname:q.params.hostname,addresses:['100.64.0.1'],backendState:'Running'});
    if (mode === 'partial') {
      // create is always the first, solitary in-flight request in this
      // test, so fragmenting only its reply exercises reassembly across
      // stdout chunks without any risk of interleaving with a concurrent
      // reply -- the real Go bridge never interleaves partial writes for
      // different in-flight requests (deliver() is mutex-serialized), so
      // that scenario would not be representative of real behavior.
      process.stdout.write(createFrame.slice(0, 10));
      setTimeout(() => process.stdout.write(createFrame.slice(10)), 10);
    } else process.stdout.write(createFrame);
    return;
  }
  const result = q.method === 'probe'
    ? {ok:false,statusCode:0,identity:'',durationMs:10,error:{code:'timeout',message:'${secret}'}}
    : {node:q.params.node,hostname:'host',addresses:['100.64.0.1','fd7a:115c:a1e0::1'],backendState:'Running'};
  process.stdout.write(reply(result));
});
`

test('serveTLS returns the certificate domain and probe forwards serverName', async () => {
  const bridge = await start('tls')
  try {
    expect(await bridge.serveTLS('one', 's')).toMatchObject({ port: 443, domain: 'host.org.example.com' })
    expect(await bridge.probe('two', 'https://100.64.0.1', 'id', 1000, 'host.org.example.com')).toMatchObject({ ok: true })
  } finally {
    await bridge.shutdown()
  }
})

async function start(mode: string) {
  return Bridge.start({ command: process.execPath, args: ['-e', mock, mode],
    rpcGraceMs: 50, shutdownGraceMs: 50, killGraceMs: 100 })
}

test('partial framing, concurrent calls, child environment, and secret-free diagnostics', async () => {
  const bridge = await start('partial')
  try {
    const node = await bridge.create({ node: 'one', hostname: 'host',
      controlURL: 'https://aaaa.ownscale.test', authKey: secret })
    expect(node.addresses).toEqual(['100.64.0.1'])
    // Calls are ID-correlated by the JSON-RPC client, not queued, so this
    // also exercises multiple requests in flight at once.
    const results = await Promise.all([bridge.status('one'), bridge.status('two'), bridge.status('three')])
    expect(results.map(r => r.node)).toEqual(['one', 'two', 'three'])
    const probe = await bridge.probe('one', 'http://100.64.0.1:80', 'identity', 100)
    expect(probe).toMatchObject({ ok: false, error: { code: 'timeout', message: 'Operation deadline exceeded' } })
    expect(bridge.diagnostics()).not.toContain(secret)
    expect(JSON.parse(bridge.diagnostics()).stderrBytes).toBeGreaterThan(65_536)
  } finally {
    await bridge.shutdown()
  }
  await bridge.shutdown()
  expect(JSON.parse(bridge.diagnostics())).toMatchObject({ exited: true, forcedCleanup: false })
})

for (const mode of ['malformed', 'wrong-id', 'wrong-jsonrpc', 'wrong-shape', 'both', 'unknown-error', 'exit', 'timeout']) {
  test(`rejects ${mode} output without leaking data or stranding queued calls`, async () => {
    const bridge = await start(mode)
    const pid = JSON.parse(bridge.diagnostics()).pid as number
    try {
      const results = await Promise.allSettled([
        bridge.probe('one', 'http://100.64.0.1:80', 'identity', 30),
        bridge.probe('two', 'http://100.64.0.1:80', 'identity', 30),
      ])
      for (const result of results) {
        expect(result.status).toBe('rejected')
        if (result.status === 'rejected') {
          expect(String(result.reason)).not.toContain(secret)
          expect(String(result.reason)).not.toContain('PRIVATE-NON-JSON')
        }
      }
    } finally {
      await expect(bridge.shutdown()).rejects.toThrow('cleanup failed')
    }
    expect(JSON.parse(bridge.diagnostics()).exited).toBe(true)
    expect(() => process.kill(pid, 0)).toThrow()
  })
}

test('structured errors are sanitized and do not poison a responsive process', async () => {
  const bridge = await start('error')
  try {
    await expect(bridge.status('missing')).rejects.toThrow('invalid_command')
    await expect(bridge.status('missing')).rejects.not.toThrow(secret)
  } finally { await bridge.shutdown() }
  expect(JSON.parse(bridge.diagnostics()).forcedCleanup).toBe(false)
})

test('non-cooperative shutdown escalates only the owned PID and reports failure', async () => {
  const bridge = await start('stubborn')
  const pid = JSON.parse(bridge.diagnostics()).pid as number
  await expect(bridge.shutdown()).rejects.toThrow('cleanup failed')
  expect(JSON.parse(bridge.diagnostics())).toMatchObject({ exited: true, forcedCleanup: true, exitSignal: 'SIGKILL' })
  expect(() => process.kill(pid, 0)).toThrow()
})

test('shutdown remains bounded behind an unresponsive in-flight call', async () => {
  const bridge = await start('timeout')
  const pending = bridge.status('one').catch(error => error)
  // The mock still replies to shutdown promptly and exits cleanly (it isn't
  // "stuck" itself); shutdown() still surfaces a failure because the
  // status('one') call was abandoned without a reply, so no force-kill is
  // needed here (unlike the 'stubborn' case above).
  await expect(bridge.shutdown()).rejects.toThrow('cleanup failed')
  expect(await pending).toBeInstanceOf(Error)
  expect(JSON.parse(bridge.diagnostics())).toMatchObject({ exited: true, forcedCleanup: false })
})

test('spawn failure is bounded and sanitized', async () => {
  await expect(Bridge.start({ command: `${root}/bin/nonexistent-bridge-${secret}`,
    helloTimeoutMs: 50, killGraceMs: 50 })).rejects.not.toThrow(secret)
})

for (const mode of ['hello-timeout', 'hello-exit', 'hello-malformed']) {
  test(`startup handles ${mode} without leaking data`, async () => {
    await expect(Bridge.start({ command: process.execPath, args: ['-e', mock, mode],
      helloTimeoutMs: 200, rpcGraceMs: 50, killGraceMs: 100 })).rejects.not.toThrow(secret)
  })
}

test('compiled Go binary negotiates hello and rejects invalid operations', async () => {
  const bridge = await Bridge.start()
  try {
    await expect(bridge.status('missing')).rejects.toThrow('unknown_handle')
  } finally { await bridge.shutdown() }
  const child = spawn(bridgeBinary, [], {
    cwd: root, env: { ...process.env, ...(caFile ? { SSL_CERT_FILE: caFile } : {}), TS_NO_LOGS_NO_SUPPORT: 'true', TMPDIR: scratchDir },
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  const closed = once(child, 'close')
  const replies: unknown[] = []
  let buffer = ''
  child.stdout.setEncoding('utf8')
  child.stdout.on('data', data => {
    buffer += data
    let newline: number
    while ((newline = buffer.indexOf('\n')) >= 0) {
      replies.push(JSON.parse(buffer.slice(0, newline)))
      buffer = buffer.slice(newline + 1)
      if (replies.length === 1) child.stdin.write(JSON.stringify({
        jsonrpc: '2.0', id: 2, method: 'shutdown', params: { timeout: '1000ms' },
      }) + '\n')
    }
  })
  child.stderr.resume()
  const timer = setTimeout(() => child.kill('SIGKILL'), 5000)
  try {
    child.stdin.write(JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'not-a-command', params: { timeout: '1000ms' } }) + '\n')
    expect(await closed).toEqual([0, null])
    expect(replies).toEqual([
      { jsonrpc: '2.0', id: 1, error: { code: 1, message: 'Invalid command or arguments', data: { code: 'invalid_command', message: 'Invalid command or arguments' } } },
      { jsonrpc: '2.0', id: 2, result: { closed: true } },
    ])
  } finally {
    clearTimeout(timer)
    if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL')
  }
})
