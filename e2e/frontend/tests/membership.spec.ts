import { randomBytes } from 'node:crypto'
import type { Browser, Page } from '@playwright/test'
import { expect, test } from './support/account'
import { confirmDialog } from './support/confirm'
import { baseDomain } from './connectivity/environment'
import { registerRealMachine, teardownRealMachine } from './support/realMachine'

// signIn signs a fresh, logged-out browser context in through Dex as email
// and returns its page, left wherever the login flow ends.
async function signIn(browser: Browser, email: string, startURL: string): Promise<Page> {
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] }, ignoreHTTPSErrors: true })
  const page = await context.newPage()
  await page.goto(startURL)
  const joinLink = page.getByRole('link', { name: 'Create account to join' })
  const ssoLink = page.getByRole('link', { name: 'Sign in with SSO' })
  // An invite URL shows a brief loading spinner while it fetches the invite
  // preview before deciding whether to render "Create account to join"
  // (invite flow) or nothing extra (direct SSO flow). Wait for whichever
  // actually shows up instead of an instant, racy isVisible() snapshot right
  // after goto, which can run before that fetch resolves.
  await joinLink.or(ssoLink).first().waitFor({ state: 'visible' })
  if (await joinLink.isVisible().catch(() => false)) {
    await joinLink.click()
    await page.getByRole('link', { name: 'Sign up with SSO' }).click()
  } else {
    await ssoLink.click()
  }
  await page.locator('#login').fill(email)
  await page.locator('#password').fill('ownscale')
  await page.locator('#submit-login').click()
  return page
}

// registerURL starts a tailnet login for org with an auth ID nobody is
// waiting on; see registerLoginStatus in settings-apply.spec.ts.
function registerURL(org: string): string {
  return `https://${org}.${baseDomain}/register/hskey-authreq-${randomBytes(12).toString('hex')}`
}

test("a user of another organization can't sign in to its tailnet until switching to a member account", async ({ browser, account }) => {
  // user1 owns bbbb (see global.setup.ts). Signing in from the tailnet's
  // login page is refused by the console...
  const page = await signIn(browser, 'user1@ownscale.test', registerURL(account.org))
  try {
    await expect(page.getByText("You aren't a member of this organization")).toBeVisible({ timeout: 15_000 })

    // ...and with a console session already in place, the OIDC provider
    // asks to sign in with another account instead of failing.
    await page.goto(registerURL(account.org))
    await expect(page.getByText("You're signed in as user1@ownscale.test")).toBeVisible()

    // Switching to a member (the worker's account owns its org) hands
    // headscale an identity.
    const callback = page.waitForRequest(
      (r) => r.isNavigationRequest() && r.url().startsWith(`https://${account.org}.${baseDomain}/oidc/callback`),
      { timeout: 30_000 },
    )
    await page.getByRole('link', { name: 'Sign in with SSO' }).click()
    await page.locator('#login').fill(account.email)
    await page.locator('#password').fill('ownscale')
    await page.locator('#submit-login').click()
    expect(new URL((await callback).url()).searchParams.get('code')).toBeTruthy()
  } finally {
    await page.context().close()
  }
})

test("removing a member removes their machines from the tailnet", async ({ page, browser, account }) => {
  test.setTimeout(300_000)

  await page.goto('/users')
  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('combobox').selectOption('member')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()
  // Until the link renders, the dialog's only textbox is the optional email
  // field; reading it then sends the invitee to sign up without the invite.
  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const inviteLink = await dialog.getByRole('textbox').inputValue()
  expect(inviteLink).toContain('/invite/')
  await dialog.getByRole('button', { name: 'Done' }).click()

  const member = await signIn(browser, account.invitee, inviteLink)
  try {
    await expect(member.getByRole('link', { name: 'Machines' })).toBeVisible({ timeout: 15_000 })
    // The member registers the machine themselves, with a key for their own user.
    const machine = await registerRealMachine(member, account.org)
    try {
      const machineNames = async () => {
        const res = await page.request.get('/api/machines')
        expect(res.ok()).toBe(true)
        return ((await res.json()) as { machines: { givenName: string }[] }).machines.map((m) => m.givenName)
      }
      expect(await machineNames()).toContain(machine.hostname)

      await page.goto('/users')
      const memberRow = page.getByRole('row').filter({ hasText: account.invitee })
      await memberRow.getByRole('button', { name: 'Remove' }).click()
      await confirmDialog(page, 'Remove member', account.invitee)
      await expect(memberRow).toBeHidden()

      expect(await machineNames()).not.toContain(machine.hostname)
    } finally {
      await teardownRealMachine(machine)
    }
  } finally {
    await member.context().close()
  }
})
