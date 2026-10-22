import { execFile } from 'node:child_process'
import { resolve } from 'node:path'
import { promisify } from 'node:util'
import { baseDomain, caFile, root } from '../connectivity/environment'

// Helpers for devbox containers: the e2e/devbox image running a real
// tailscaled, joined to an organization's tailnet (see `make devbox`).

const execFileAsync = promisify(execFile)

export const devboxImage = 'ownscale-devbox:latest'

// docker runs a docker CLI command from the repository root and returns its
// trimmed stdout. It throws on a non-zero exit or after timeoutMs.
export async function docker(args: string[], timeoutMs = 60_000): Promise<string> {
  const { stdout } = await execFileAsync('docker', args, { cwd: root, timeout: timeoutMs })
  return stdout.trim()
}

export async function ensureDevboxImage(): Promise<void> {
  await docker(['build', '-t', devboxImage, resolve(root, 'e2e/devbox')], 10 * 60_000)
}

// DevboxOptions customizes startDevbox.
export interface DevboxOptions {
  // upFlags are passed to `tailscale up`.
  upFlags?: string[]
  // dockerArgs are extra `docker run` arguments, e.g. sysctls.
  dockerArgs?: string[]
  // networks are extra docker networks connected before joining the tailnet.
  networks?: string[]
}

// startDevbox starts container name and joins it to org's tailnet as
// hostname. Its node belongs to org's first headscale user and is
// ephemeral, so headscale drops it after the container is removed.
export async function startDevbox(name: string, org: string, hostname: string, opts: DevboxOptions = {}): Promise<void> {
  if (!caFile) throw new Error('devbox containers need the local dev CA; unset E2E_CA_FILE only for public stacks')
  await docker([
    'run', '-d', '--rm', '--init', '--name', name, '--network', await stackNetwork(),
    '--cap-add=NET_ADMIN', '--cap-add=NET_RAW', '--device=/dev/net/tun',
    `--add-host=${org}.${baseDomain}:host-gateway`,
    '-v', `${caFile}:/usr/local/share/ca-certificates/ownscale-local-ca.crt:ro`,
    ...(opts.dockerArgs ?? []),
    devboxImage, 'sleep', 'infinity',
  ])
  for (const network of opts.networks ?? []) await docker(['network', 'connect', network, name])
  const authKey = await docker(['compose', 'exec', '-T', 'ownscale', 'osa', 'devbox-key', org])
  await docker(['exec', name, 'devbox-up', `https://${org}.${baseDomain}`, authKey, hostname, ...(opts.upFlags ?? [])], 60_000)
}

// stackNetwork returns the dev stack's docker network. Devboxes join it
// rather than docker's default bridge: that bridge goes up when its first
// container starts and down when its last one stops, which Chromium reports
// as a network change, aborting requests of browsers testing concurrently
// (net::ERR_NETWORK_CHANGED). The stack's own network always has containers.
let network: Promise<string> | undefined
function stackNetwork(): Promise<string> {
  network ??= (async () => {
    const id = await docker(['compose', 'ps', '-q', 'ownscale'])
    const names = await docker(['inspect', id, '--format', '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}'])
    const name = names.split(' ')[0]
    if (!name) throw new Error('the ownscale container is on no docker network; is the dev stack running?')
    return name
  })()
  return network
}

// removeDevbox force-removes container name, ignoring a missing container.
export async function removeDevbox(name: string): Promise<void> {
  await docker(['rm', '-f', name]).catch(() => {})
}

// createPrivateNetwork creates an internal docker network, reachable only
// from containers attached to it, and returns its IPv4 subnet.
export async function createPrivateNetwork(name: string): Promise<string> {
  await docker(['network', 'create', '--internal', name])
  return docker(['network', 'inspect', name, '--format', '{{range .IPAM.Config}}{{.Subnet}} {{end}}'])
    .then(out => out.split(' ').find(s => s.includes('.')) ?? '')
}

export async function removeNetwork(name: string): Promise<void> {
  await docker(['network', 'rm', name]).catch(() => {})
}

// startWebHost starts container name on network only, serving body over
// HTTP on port 8080, and returns its IP on that network.
export async function startWebHost(name: string, network: string, body: string): Promise<string> {
  await docker([
    'run', '-d', '--rm', '--init', '--name', name, '--network', network, '-e', `BODY=${body}`, devboxImage,
    'sh', '-c', 'mkdir -p /tmp/www && printf %s "$BODY" > /tmp/www/index.html && cd /tmp/www && exec python3 -m http.server 8080',
  ])
  return docker(['inspect', name, '--format', `{{(index .NetworkSettings.Networks "${network}").IPAddress}}`])
}
