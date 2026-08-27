import { test, expect } from '@playwright/test'
import { INVITEE_EMAIL, loginViaDex } from '../lib/dex'

test('invites a member, who accepts via their own OIDC login', async ({ page, browser }) => {
  await page.goto('/users')

  await page.getByRole('button', { name: 'Invite member' }).click()
  const inviteDialog = page.locator('dialog', { hasText: 'Invite a member' })
  await inviteDialog.getByRole('button', { name: 'Create invite link' }).click()

  const linkInput = inviteDialog.locator('input[type="text"][readonly]')
  await expect(linkInput).toBeVisible()
  const inviteLink = await linkInput.inputValue()
  expect(inviteLink).toContain('/invite/')
  await inviteDialog.getByRole('button', { name: 'Done' }).click()

  // Accept the invite as a second, independent user: a fresh browser
  // context with no storageState, logging in as a different Dex static
  // user (user1@ownscale.test) via the real OIDC flow.
  const inviteeContext = await browser.newContext({ ignoreHTTPSErrors: true, storageState: undefined })
  const inviteePage = await inviteeContext.newPage()
  try {
    await inviteePage.goto(inviteLink)
    // AcceptInvite.tsx bounces an unauthenticated visitor through
    // /login?next=/invite/:token first.
    await expect(inviteePage).toHaveURL(/\/login/)
    await loginViaDex(inviteePage, INVITEE_EMAIL)

    await expect(inviteePage).toHaveURL(/\/invite\//)
    await inviteePage.getByRole('button', { name: 'Accept invite' }).click()
    await expect(inviteePage).toHaveURL(/\/(machines|users|access-control)?$/)
  } finally {
    await inviteeContext.close()
  }

  // Back in the admin context, the new member now shows up in the list.
  await page.reload()
  await expect(page.locator('table').first().getByText(INVITEE_EMAIL)).toBeVisible()
})
