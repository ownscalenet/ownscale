import { randomBytes } from 'node:crypto'
import { expect, test, type Page } from '@playwright/test'
import { docker, ensureDevboxImage, removeDevbox, startDevbox } from '../support/devbox'

// admin's organization. `osa devbox-key` mints keys for its first headscale
// user, so both devboxes below belong to the same user.
const org = 'aaaa'

const allowAll = `"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],`
const withSSH = `{
  ${allowAll}
  "ssh": [
    {"action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:self"], "users": ["root"]},
  ],
}`
const withoutSSH = `{
  ${allowAll}
}`

async function getPolicy(page: Page): Promise<string> {
  const res = await page.request.get('/api/access-control')
  expect(res.ok(), await res.text()).toBe(true)
  return ((await res.json()) as { content: string }).content
}

async function putPolicy(page: Page, content: string): Promise<void> {
  const res = await page.request.put('/api/access-control', { data: { content } })
  expect(res.ok(), await res.text()).toBe(true)
}

test('Tailscale SSH works between two machines only while the policy allows it', async ({ page }) => {
  test.setTimeout(10 * 60_000)
  await ensureDevboxImage()

  const suffix = randomBytes(3).toString('hex')
  const server = `ownscale-e2e-ssh-srv-${suffix}`
  const client = `ownscale-e2e-ssh-cli-${suffix}`
  const original = await getPolicy(page)

  // sshEcho runs a command over Tailscale SSH from client to serverIP and
  // reports "ok" or why it didn't work.
  let serverIP = ''
  async function sshEcho(): Promise<string> {
    try {
      const out = await docker(['exec', client, 'tailscale', 'ssh', `root@${serverIP}`, 'echo', 'ssh-ok'], 20_000)
      return out === 'ssh-ok' ? 'ok' : `unexpected output: ${out}`
    } catch (err) {
      const stderr = (err as { stderr?: string }).stderr?.trim()
      return `failed: ${stderr || String(err)}`
    }
  }

  try {
    await putPolicy(page, withSSH)
    await startDevbox(server, org, `ssh-srv-${suffix}`)
    await startDevbox(client, org, `ssh-cli-${suffix}`)
    serverIP = await docker(['exec', server, 'tailscale', 'ip', '-4'])

    await expect.poll(sshEcho, { timeout: 90_000, intervals: [2_000] }).toBe('ok')

    // Only the ssh rule grants access: without it the same command fails.
    await putPolicy(page, withoutSSH)
    await expect
      .poll(async () => ((await sshEcho()) === 'ok' ? 'allowed' : 'denied'), { timeout: 90_000, intervals: [2_000] })
      .toBe('denied')
  } finally {
    await removeDevbox(client)
    await removeDevbox(server)
    await putPolicy(page, original)
  }
})
