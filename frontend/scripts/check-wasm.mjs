// Starts the Tailscale WebAssembly client under Go's Node runner and fails if
// it panics during initialization. Upstream builds of @tailscale/connect have
// shipped with a Go toolchain that doesn't match the one the module expects
// (a panic in tailscale.com's init), which only shows up in the browser.
// Without a JS host the client then stops with a deadlock, which is fine here.
import { execFileSync, spawnSync } from 'node:child_process'
import path from 'node:path'

const wasm = path.resolve(
  process.argv[2] ?? path.join(import.meta.dirname, '../node_modules/@tailscale/connect/main.wasm'),
)
const goroot = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim()
const runner = path.join(goroot, 'lib/wasm/wasm_exec_node.js')

const res = spawnSync(process.execPath, [runner, wasm], { encoding: 'utf8', timeout: 60_000 })
if (res.error) throw res.error

const output = res.stdout + res.stderr
if (output.includes('panic:')) {
  console.error(`${wasm} panics on startup:\n${output}`)
  process.exit(1)
}
console.log('main.wasm starts without panicking')
