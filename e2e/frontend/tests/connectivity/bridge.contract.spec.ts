import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { Bridge } from './bridge'
import { bridgeBinary, caFile, root, scratchDir } from './environment'

const secret = 'contract-private-key-cookie-password'
const mock = `
const readline = require('node:readline');
const mode = process.argv[1];
let count = 0;
if (mode === 'stubborn') process.on('SIGTERM', () => {});
readline.createInterface({ input: process.stdin }).on('line', line => {
  const q = JSON.parse(line);
  const reply = result => JSON.stringify({ version: 1, id: q.id, result }) + '\\n';
  if (q.op === 'hello') {
    if (mode === 'hello-timeout') return;
    if (mode === 'hello-exit') { process.exit(0); return; }
    if (mode === 'hello-malformed') { process.stdout.write('${secret}\\n'); return; }
    process.stdout.write(reply({ ready: true, maxTimeoutMs: 300000, maxFrameBytes: 65536 }));
    return;
  }
  if (q.op === 'shutdown') {
    if (mode === 'stubborn') { setInterval(() => {}, 1000); return; }
    process.stdout.write(reply({ closed: true }), () => process.exit(0));
    return;
  }
  if (mode === 'exit') { process.stdout.write('{"version":'); process.exit(7); }
  if (mode === 'timeout') return;
  if (mode === 'malformed') { process.stdout.write('PRIVATE-NON-JSON\\n'); return; }
  if (mode === 'oversize') { process.stdout.write('x'.repeat(65537)); return; }
  if (mode === 'wrong-id') { process.stdout.write(JSON.stringify({version:1,id:'wrong',result:{}})+'\\n'); return; }
  if (mode === 'wrong-version') { process.stdout.write(JSON.stringify({version:2,id:q.id,result:{}})+'\\n'); return; }
  if (mode === 'wrong-shape') { process.stdout.write(reply({ok:true})); return; }
  if (mode === 'both') { process.stdout.write(JSON.stringify({version:1,id:q.id,result:{},error:{code:'timeout',message:'SECRET'}})+'\\n'); return; }
  if (mode === 'error') { process.stdout.write(JSON.stringify({version:1,id:q.id,error:{code:'invalid_command',message:'${secret}'}})+'\\n'); return; }
  if (mode === 'unknown-error') { process.stdout.write(JSON.stringify({version:1,id:q.id,error:{code:'${secret}',message:'${secret}'}})+'\\n'); return; }
  process.stderr.write('${secret}'.repeat(5000));
  if (q.op === 'create') {
    if (q.args.authKey !== '${secret}' || process.argv.includes(q.args.authKey) ||
        process.env.SSL_CERT_FILE !== ${caFile ? JSON.stringify(caFile) : 'undefined'} || process.env.TS_NO_LOGS_NO_SUPPORT !== 'true')
      process.exit(9);
    process.stdout.write(reply({node:q.args.node,hostname:q.args.hostname,addresses:['100.64.0.1'],backendState:'Running'}));
    return;
  }
  const result = q.op === 'probe'
    ? {ok:false,statusCode:0,identity:'',durationMs:10,error:{code:'timeout',message:'${secret}'}}
    : {node:q.args.node,hostname:'host',addresses:['100.64.0.1','fd7a:115c:a1e0::1'],backendState:'Running'};
  const frame = reply(result);
  if (mode === 'partial') {
    process.stdout.write(frame.slice(0, 10));
    setTimeout(() => process.stdout.write(frame.slice(10)), 10);
  } else process.stdout.write(frame);
});
`

async function start(mode: string) {
  return Bridge.start({ command: process.execPath, args: ['-e', mock, mode],
    rpcGraceMs: 50, shutdownGraceMs: 50, killGraceMs: 100 })
}

test('partial framing, serialized calls, child environment, and secret-free diagnostics', async () => {
  const bridge = await start('partial')
  try {
    const node = await bridge.create({ node: 'one', hostname: 'host',
      controlURL: 'https://aaaa.ownscale.test', authKey: secret })
    expect(node.addresses).toEqual(['100.64.0.1'])
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

for (const mode of ['malformed', 'oversize', 'wrong-id', 'wrong-version', 'wrong-shape', 'both', 'unknown-error', 'exit', 'timeout']) {
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
  await expect(bridge.shutdown()).rejects.toThrow('cleanup failed')
  expect(await pending).toBeInstanceOf(Error)
  expect(JSON.parse(bridge.diagnostics())).toMatchObject({ exited: true, forcedCleanup: true })
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
        version: 1, id: 'stop', op: 'shutdown', timeoutMs: 1000, args: {},
      }) + '\n')
    }
  })
  child.stderr.resume()
  const timer = setTimeout(() => child.kill('SIGKILL'), 5000)
  try {
    child.stdin.write(JSON.stringify({ version: 1, id: 'invalid', op: 'not-a-command', timeoutMs: 1000, args: {} }) + '\n')
    expect(await closed).toEqual([0, null])
    expect(replies).toEqual([
      { version: 1, id: 'invalid', error: { code: 'invalid_command', message: 'Invalid command, arguments, framing, or request ID' } },
      { version: 1, id: 'stop', result: { closed: true } },
    ])
  } finally {
    clearTimeout(timer)
    if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL')
  }
})
