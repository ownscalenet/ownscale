import { randomBytes } from 'node:crypto'
import type { Page } from '@playwright/test'
import { expect, test } from '../support/account'
import { ensureDevboxImage, removeDevbox, startDevbox } from '../support/devbox'

const policyWithSSH = `{
  "acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],
  "ssh": [
    {"action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:self"], "users": ["root"]},
  ],
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

// The browser SSH page joins the tailnet as an ephemeral node owned by the
// signed-in user, the same headscale user `osa devbox-key` mints keys for,
// so autogroup:self covers both ends.
test('SSH from the browser into a real machine', async ({ page, account }) => {
  test.setTimeout(3 * 60_000)
  await ensureDevboxImage()

  const suffix = randomBytes(3).toString('hex')
  const server = `ownscale-e2e-websh-${suffix}`
  const hostname = `websh-${suffix}`
  const original = await getPolicy(page)

  // The wasm client logs its whole tailnet handshake to the console, which
  // is the only way to debug a failed connection: E2E_BROWSER_LOGS=1 keeps it.
  if (process.env.E2E_BROWSER_LOGS) {
    page.on('console', msg => console.log(`[browser:${msg.type()}] ${msg.text()}`))
  }
  page.on('pageerror', err => console.log(`[browser:pageerror] ${err.message}`))

  // The upstream wasm build uploads client logs to Tailscale's log
  // service; the SSH page stubs those out (muteTailscaleLogs).
  const telemetry: string[] = []
  page.on('request', req => {
    if (req.url().includes('log.tailscale.com')) telemetry.push(req.url())
  })

  try {
    await putPolicy(page, policyWithSSH)
    await startDevbox(server, account.org, hostname)

    await page.goto('/machines')
    const row = page.getByRole('row').filter({ hasText: hostname })
    await expect(row).toBeVisible({ timeout: 30_000 })
    await row.getByRole('button', { name: 'Actions', exact: true }).click()
    await page.getByRole('menuitem', { name: 'SSH', exact: true }).click()

    await page.waitForURL(/\/machines\/\d+\/ssh$/, { timeout: 10_000 })
    const pageStyle = () =>
      page.evaluate(() => {
        const html = getComputedStyle(document.documentElement)
        const title = getComputedStyle(document.querySelector('h1')!)
        return [html.fontFamily, html.lineHeight, title.fontSize, title.fontWeight].join('|')
      })
    const styleBeforeSSH = await pageStyle()
    await page.getByRole('textbox').fill('root')
    await page.getByRole('button', { name: 'Connect', exact: true }).click()

    // The wasm client is ~27MB and has to join the tailnet before the
    // terminal opens, so give it room.
    const terminal = page.locator('.xterm-screen')
    await expect(terminal).toBeVisible({ timeout: 60_000 })
    await expect(page.locator('.xterm-rows')).toContainText('#', { timeout: 60_000 })

    await page.keyboard.type('echo browser-ssh-ok')
    await page.keyboard.press('Enter')
    await expect(page.locator('.xterm-rows')).toContainText('browser-ssh-ok', { timeout: 10_000 })

    // The terminal's stylesheet must not restyle the console around it.
    expect(await pageStyle()).toBe(styleBeforeSSH)

    expect(telemetry).toEqual([])
  } finally {
    await removeDevbox(server)
    await putPolicy(page, original)
  }
})
