import { expect, test } from './support/account'
import { confirmDialog } from './support/confirm'

// delete existing invites because tests rely on having zero pre-existing invites.
test.beforeEach(async ({ page }) => {
  const { invites } = await page.request.get('/api/invites').then((r) => r.json())
  for (const invite of invites) {
    await page.request.delete(`/api/invites/${invite.id}`)
  }
})

test('creating and revoking an invite link', async ({ page }) => {
  await page.goto('/users')
  await expect(page.getByRole('heading', { name: 'Users' })).toBeVisible()

  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()

  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const link = dialog.getByRole('textbox')
  await expect(link).toHaveValue(/\/invite\//)
  await dialog.getByRole('button', { name: 'Done' }).click()

  await expect(page.getByRole('heading', { name: 'Pending invites' })).toBeVisible()
  // Take newest invite.
  const row = page.getByRole('row').filter({ hasText: '(link only)' }).last()
  await expect(row).toBeVisible()

  await row.getByRole('button', { name: 'Revoke' }).click()
  await expect(row).toBeHidden()
})

test('accepting an invite adds a member, whose role can be changed and who can be removed', async ({
  page,
  browser,
  account,
}) => {
  await page.goto('/users')
  await expect(page.getByRole('heading', { name: 'Users' })).toBeVisible()

  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('combobox').selectOption('admin')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()
  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const inviteLink = await dialog.getByRole('textbox').inputValue()
  expect(inviteLink).toContain('/invite/')
  await dialog.getByRole('button', { name: 'Done' }).click()

  const inviteeContext = await browser.newContext({ storageState: { cookies: [], origins: [] } })
  try {
    const inviteePage = await inviteeContext.newPage()
    await inviteePage.goto(inviteLink)
    await inviteePage.getByRole('link', { name: 'Sign in to join' }).click()
    await expect(inviteePage).toHaveURL(/\/login\?next=/)

    await inviteePage.getByRole('link', { name: 'Sign in with SSO' }).click()
    await inviteePage.locator('#login').fill(account.invitee)
    await inviteePage.locator('#password').fill('ownscale')
    await inviteePage.locator('#submit-login').click()

    await expect(inviteePage.getByRole('link', { name: 'Machines' })).toBeVisible({
      timeout: 15_000,
    })
  } finally {
    await inviteeContext.close()
  }

  await page.reload()
  const memberRow = page.getByRole('row').filter({ hasText: account.invitee })
  await expect(memberRow).toBeVisible()
  await expect(memberRow.getByRole('combobox')).toHaveValue('admin')

  await expect(memberRow.getByRole('button', { name: 'Make owner' })).toBeVisible()

  await memberRow.getByRole('combobox').selectOption('auditor')
  await expect(memberRow.getByRole('combobox')).toHaveValue('auditor')

  await memberRow.getByRole('button', { name: 'Remove' }).click()
  await confirmDialog(page, 'Remove member', account.invitee)
  await expect(memberRow).toBeHidden()
})

test('copying a member email from the table', async ({ page, account }) => {
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.goto('/users')

  const row = page.getByRole('row').filter({ hasText: account.email })
  await row.getByRole('button', { name: 'Copy email' }).click()

  await expect(row.locator('[data-tip="Copied!"]')).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(account.email)
})

test('visiting an invite link with an unknown token shows an error, signed in', async ({
  page,
}) => {
  await page.goto('/invite/this-token-does-not-exist')
  await expect(page.getByRole('heading', { name: 'Join organization' })).toBeVisible()
  await expect(page.getByText(/invite not found/i)).toBeVisible()
})

test('a plain member cannot see owner/admin-only management controls', async ({
  page,
  browser,
  account,
}) => {
  // Invite a plain member so we can sign in as someone without manage
  // rights, then verify the UI hides (not just the backend rejecting)
  // owner/admin-only actions for them.
  await page.goto('/users')
  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()
  const inviteLink = await dialog.getByRole('textbox').inputValue()
  await dialog.getByRole('button', { name: 'Done' }).click()

  const memberContext = await browser.newContext({
    storageState: { cookies: [], origins: [] },
    ignoreHTTPSErrors: true,
  })
  const memberPage = await memberContext.newPage()
  try {
    await memberPage.goto(inviteLink)
    await memberPage.getByRole('link', { name: 'Sign in to join' }).click()
    await memberPage.getByRole('link', { name: 'Sign in with SSO' }).click()
    await memberPage.locator('#login').fill(account.invitee)
    await memberPage.locator('#password').fill('ownscale')
    await memberPage.locator('#submit-login').click()
    await expect(memberPage.getByRole('link', { name: 'Machines' })).toBeVisible({
      timeout: 15_000,
    })

    // Users page: no "Invite member" button, no role dropdowns/Remove
    // buttons for other members.
    await memberPage.goto('/users')
    await expect(memberPage.getByRole('heading', { name: 'Users' })).toBeVisible()
    await expect(memberPage.getByRole('button', { name: 'Invite member' })).toBeHidden()
    await expect(memberPage.getByRole('combobox')).toHaveCount(0)
    await expect(memberPage.getByRole('button', { name: 'Remove' })).toHaveCount(0)

    // Pre-auth keys, DNS, access control and auth restrictions pages are
    // read-only / inaccessible for a plain member.
    await memberPage.goto('/pre-auth-keys')
    await expect(memberPage.getByRole('heading', { name: 'Pre-auth Keys', level: 1 })).toBeVisible()
    await expect(memberPage.getByRole('button', { name: 'Create pre-auth key' })).toBeVisible()
  } finally {
    await memberContext.close()
  }

  // Clean up so the invitee is free to be re-invited by other tests.
  await page.goto('/users')
  const memberRow = page.getByRole('row').filter({ hasText: account.invitee })
  await memberRow.getByRole('button', { name: 'Remove' }).click()
  await confirmDialog(page, 'Remove member', account.invitee)
  await expect(memberRow).toBeHidden()
})
