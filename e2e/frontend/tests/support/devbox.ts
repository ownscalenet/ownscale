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

// startDevbox starts container name and joins it to org's tailnet as
// hostname, passing upFlags to `tailscale up`. Its node belongs to org's
// first headscale user and is ephemeral, so headscale drops it after the
// container is removed.
export async function startDevbox(name: string, org: string, hostname: string, upFlags: string[] = []): Promise<void> {
  if (!caFile) throw new Error('devbox containers need the local dev CA; unset E2E_CA_FILE only for public stacks')
  await docker([
    'run', '-d', '--rm', '--init', '--name', name, '--network', await stackNetwork(),
    '--cap-add=NET_ADMIN', '--cap-add=NET_RAW', '--device=/dev/net/tun',
    `--add-host=${org}.${baseDomain}:host-gateway`,
    '-v', `${caFile}:/usr/local/share/ca-certificates/ownscale-local-ca.crt:ro`,
    devboxImage, 'sleep', 'infinity',
  ])
  const authKey = await docker(['compose', 'exec', '-T', 'ownscale', 'osa', 'devbox-key', org])
  await docker(['exec', name, 'devbox-up', `https://${org}.${baseDomain}`, authKey, hostname, ...upFlags], 60_000)
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
