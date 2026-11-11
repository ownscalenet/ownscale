import type { Browser, Page } from '@playwright/test'
import { emailOf, expect, test } from './support/account'
import { confirmDialog } from './support/confirm'
import { baseDomain, localTLS } from './connectivity/environment'
import { startPendingLogin } from './support/pendingLogin'
import { registerRealMachine, teardownRealMachine } from './support/realMachine'

// signIn signs a fresh, logged-out browser context in through Dex as email
// and returns its page, left wherever the login flow ends.
async function signIn(browser: Browser, email: string, startURL: string): Promise<Page> {
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] }, ignoreHTTPSErrors: localTLS })
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

test("a user of another organization can't sign in to its tailnet until switching to a member account", async ({ browser, account }) => {
  const login = await startPendingLogin(account.org)
  // bbbb owns another org. Signing in from the tailnet's login page is
  // refused by the console...
  const outsider = emailOf('bbbb')
  const page = await signIn(browser, outsider, await login.authURL())
  try {
    await expect(page.getByText("You aren't a member of this organization")).toBeVisible({ timeout: 15_000 })

    // ...and with a console session already in place, the OIDC provider
    // asks to sign in with another account instead of failing.
    await page.goto(await login.authURL())
    await expect(page.getByText(`You're signed in as ${outsider}`)).toBeVisible()

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
    await login.close()
  }
})

test("removing a member removes their machines from the tailnet", async ({ page, browser, account }) => {
  test.setTimeout(300_000)

  await page.goto('/invitations')
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
      await confirmDialog(page, 'Remove member')
      await expect(memberRow).toBeHidden()

      expect(await machineNames()).not.toContain(machine.hostname)
    } finally {
      await teardownRealMachine(machine)
    }
  } finally {
    await member.context().close()
  }
})

test('a member deletes their own account, which the owner can only do once alone', async ({ page, browser, account }) => {
  test.setTimeout(300_000)

  await page.goto('/invitations')
  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('combobox').selectOption('member')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()
  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const inviteLink = await dialog.getByRole('textbox').inputValue()
  expect(inviteLink).toContain('/invite/')
  await dialog.getByRole('button', { name: 'Done' }).click()

  const member = await signIn(browser, account.invitee, inviteLink)
  try {
    await expect(member.getByRole('link', { name: 'Machines' })).toBeVisible({ timeout: 15_000 })

    // With a member in the org, the owner is pointed at the Users page
    // instead, and the server refuses too.
    await page.goto('/user-settings')
    const deleteButton = page.getByRole('button', { name: 'Delete account' })
    await expect(page.getByText('You own this organization and it still has other members.')).toBeVisible()
    await expect(page.getByRole('main').getByRole('link', { name: 'Users' })).toHaveAttribute('href', '/users')
    await expect(deleteButton).toBeDisabled()
    expect((await page.request.delete('/api/account')).status()).toBe(409)

    const machine = await registerRealMachine(member, account.org)
    try {
      const machineNames = async () => {
        const res = await page.request.get('/api/machines')
        expect(res.ok()).toBe(true)
        return ((await res.json()) as { machines: { givenName: string }[] }).machines.map((m) => m.givenName)
      }
      expect(await machineNames()).toContain(machine.hostname)

      await member.goto('/user-settings')
      await member.getByRole('button', { name: 'Delete account' }).click()
      await confirmDialog(member, 'Delete account')
      await expect(member).toHaveURL(/\/login/, { timeout: 15_000 })
      expect((await member.request.get('/api/session')).status()).toBe(401)

      const members = (await page.request.get('/api/members').then((r) => r.json())) as { members: { email: string }[] }
      expect(members.members.map((m) => m.email)).not.toContain(account.invitee)
      expect(await machineNames()).not.toContain(machine.hostname)

      // Alone again, the owner may delete their account. Not clicked: the
      // worker's account is shared with the other tests.
      await page.reload()
      await expect(deleteButton).toBeEnabled()
      await expect(page.getByText('You own this organization and it still has other members.')).toBeHidden()
      await expect(page.getByText(/delete your account and this organization/)).toBeVisible()
    } finally {
      await teardownRealMachine(machine)
    }
  } finally {
    await member.context().close()
  }
})

test('a member sees the tailnet name on the DNS page but cannot change DNS settings', async ({ page, browser, account }) => {
  await page.goto('/invitations')
  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('combobox').selectOption('member')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()
  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const inviteLink = await dialog.getByRole('textbox').inputValue()
  expect(inviteLink).toContain('/invite/')
  await dialog.getByRole('button', { name: 'Done' }).click()

  const ownerDNS = (await (await page.request.get('/api/dns')).json()).dns
  const member = await signIn(browser, account.invitee, inviteLink)
  try {
    await expect(member.getByRole('link', { name: 'Machines' })).toBeVisible({ timeout: 15_000 })
    await member.goto('/dns')
    await expect(member.getByText('Only owners and admins can edit DNS settings')).toBeVisible()
    await expect(
      member.locator('.card', { hasText: 'Tailnet Name' }).locator('.font-mono'),
    ).toHaveText(ownerDNS.baseDomain)
    expect((await member.request.put('/api/dns', { data: ownerDNS })).status()).toBe(403)
  } finally {
    await member.context().close()
    // Invites only apply to brand-new accounts, so remove the member again.
    await page.goto('/users')
    const memberRow = page.getByRole('row').filter({ hasText: account.invitee })
    await memberRow.getByRole('button', { name: 'Remove' }).click()
    await confirmDialog(page, 'Remove member')
    await expect(memberRow).toBeHidden()
  }
})
