import { expect, test } from './support/account'
import { localTLS } from './connectivity/environment'
import { confirmDialog } from './support/confirm'
import { addVirtualAuthenticator } from './support/webauthn'

// delete existing invites because tests rely on having zero pre-existing invites.
test.beforeEach(async ({ page }) => {
  const { invites } = await page.request.get('/api/invites').then((r) => r.json())
  for (const invite of invites) {
    await page.request.delete(`/api/invites/${invite.id}`)
  }
})

test('creating and revoking an invite link', async ({ page }) => {
  await page.goto('/invitations')
  await expect(page.getByRole('heading', { name: 'Invitations' })).toBeVisible()

  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()

  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const link = dialog.getByRole('textbox')
  await expect(link).toHaveValue(/\/invite\//)
  await dialog.getByRole('button', { name: 'Done' }).click()

  // Take newest invite.
  const row = page.getByRole('row').filter({ hasText: '(link only)' }).last()
  await expect(row).toBeVisible()

  await row.getByRole('button', { name: 'Revoke' }).click()
  await expect(row).toBeHidden()
})

test('pasting multiple names creates one invite per name', async ({ page }) => {
  await page.goto('/invitations')
  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('textbox').fill('Alice\nBob\nCarol\nAlice')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()

  await expect(dialog.getByText(/expire in 7 days/i)).toBeVisible()
  const rows = dialog.getByRole('listitem')
  await expect(rows).toHaveCount(3)
  await expect(rows.filter({ hasText: 'Alice' })).toBeVisible()
  await expect(rows.filter({ hasText: 'Bob' })).toBeVisible()
  await expect(rows.filter({ hasText: 'Carol' })).toBeVisible()
  await expect(rows.getByRole('button', { name: 'Copy link' })).toHaveCount(3)
  await dialog.getByRole('button', { name: 'Done' }).click()

  for (const name of ['Alice', 'Bob', 'Carol']) {
    await expect(page.getByRole('row').filter({ hasText: name })).toBeVisible()
  }
})

test('accepting an invite adds a member, whose role can be changed and who can be removed', async ({
  page,
  browser,
  account,
}) => {
  await page.goto('/invitations')
  await expect(page.getByRole('heading', { name: 'Invitations' })).toBeVisible()

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
    await inviteePage.getByRole('link', { name: 'Create account to join' }).click()
    await expect(inviteePage).toHaveURL(/\/signup\?next=/)

    await inviteePage.getByRole('link', { name: 'Sign up with SSO' }).click()
    await inviteePage.locator('#login').fill(account.invitee)
    await inviteePage.locator('#password').fill('ownscale')
    await inviteePage.locator('#submit-login').click()

    await expect(inviteePage.getByRole('link', { name: 'Machines' })).toBeVisible({
      timeout: 15_000,
    })
  } finally {
    await inviteeContext.close()
  }

  await page.goto('/users')
  const memberRow = page.getByRole('row').filter({ hasText: account.invitee })
  await expect(memberRow).toBeVisible()
  await expect(memberRow.getByRole('combobox')).toHaveValue('admin')

  await expect(memberRow.getByRole('button', { name: 'Make owner' })).toBeVisible()

  await memberRow.getByRole('combobox').selectOption('auditor')
  await expect(memberRow.getByRole('combobox')).toHaveValue('auditor')

  await memberRow.getByRole('button', { name: 'Remove' }).click()
  await confirmDialog(page, 'Remove member')
  await expect(memberRow).toBeHidden()
})

test('creating an account from an invite joins the inviting organization', async ({
  page,
  browser,
}) => {
  await page.goto('/invitations')
  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()
  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const inviteLink = await dialog.getByRole('textbox').inputValue()
  expect(inviteLink).toContain('/invite/')
  await dialog.getByRole('button', { name: 'Done' }).click()

  // Unique per run: the account outlives the membership removed below.
  const username = `invitee${Date.now()}`
  const inviteeContext = await browser.newContext({ storageState: { cookies: [], origins: [] } })
  try {
    const inviteePage = await inviteeContext.newPage()
    await addVirtualAuthenticator(inviteePage)
    await inviteePage.goto(inviteLink)
    await inviteePage.getByRole('link', { name: 'Create account to join' }).click()
    await expect(inviteePage).toHaveURL(/\/signup\?next=%2Finvite%2F/)
    await expect(inviteePage.getByText('Create an account to continue.')).toBeVisible()

    await inviteePage.locator('#passkey-username').fill(username)
    await inviteePage.getByRole('button', { name: 'Continue with passkey' }).click()

    await expect(inviteePage.getByRole('link', { name: 'Machines' })).toBeVisible({
      timeout: 15_000,
    })
  } finally {
    await inviteeContext.close()
  }

  await page.goto('/users')
  const memberRow = page.getByRole('row').filter({ hasText: username })
  await expect(memberRow).toBeVisible()

  await memberRow.getByRole('button', { name: 'Remove' }).click()
  await confirmDialog(page, 'Remove member')
  await expect(memberRow).toBeHidden()
})

test('transferring ownership promotes the member and demotes the caller to admin', async ({
  page,
  browser,
  account,
}) => {
  await page.goto('/invitations')
  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()
  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const inviteLink = await dialog.getByRole('textbox').inputValue()
  expect(inviteLink).toContain('/invite/')
  await dialog.getByRole('button', { name: 'Done' }).click()

  const memberContext = await browser.newContext({
    storageState: { cookies: [], origins: [] },
    ignoreHTTPSErrors: localTLS,
  })
  try {
    const memberPage = await memberContext.newPage()
    await memberPage.goto(inviteLink)
    await memberPage.getByRole('link', { name: 'Create account to join' }).click()
    await memberPage.getByRole('link', { name: 'Sign up with SSO' }).click()
    await memberPage.locator('#login').fill(account.invitee)
    await memberPage.locator('#password').fill('ownscale')
    await memberPage.locator('#submit-login').click()
    await expect(memberPage.getByRole('link', { name: 'Machines' })).toBeVisible({
      timeout: 15_000,
    })

    // The owner transfers ownership to the new member.
    await page.goto('/users')
    const memberRow = page.getByRole('row').filter({ hasText: account.invitee })
    await memberRow.getByRole('button', { name: 'Make owner' }).click()
    await confirmDialog(page, 'Transfer ownership')
    await expect(memberRow.getByText('Owner', { exact: true })).toBeVisible()
    const callerRow = page.getByRole('row').filter({ hasText: account.email })
    await expect(callerRow.getByRole('combobox')).toHaveValue('admin')
    // Only the owner can transfer ownership, so the demoted caller no
    // longer sees any "Make owner" button.
    await expect(page.getByRole('button', { name: 'Make owner' })).toHaveCount(0)

    // The new owner transfers it back, so the worker's account stays the
    // owner for every other spec sharing this org.
    await memberPage.goto('/users')
    const originalOwnerRow = memberPage.getByRole('row').filter({ hasText: account.email })
    await originalOwnerRow.getByRole('button', { name: 'Make owner' }).click()
    await confirmDialog(memberPage, 'Transfer ownership')
    await expect(originalOwnerRow.getByText('Owner', { exact: true })).toBeVisible()
  } finally {
    await memberContext.close()
  }

  // Clean up so the invitee is free to be re-invited by other tests.
  await page.reload()
  const memberRow = page.getByRole('row').filter({ hasText: account.invitee })
  await memberRow.getByRole('button', { name: 'Remove' }).click()
  await confirmDialog(page, 'Remove member')
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
  await page.goto('/invitations')
  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()
  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const inviteLink = await dialog.getByRole('textbox').inputValue()
  expect(inviteLink).toContain('/invite/')
  await dialog.getByRole('button', { name: 'Done' }).click()

  const memberContext = await browser.newContext({
    storageState: { cookies: [], origins: [] },
    ignoreHTTPSErrors: localTLS,
  })
  const memberPage = await memberContext.newPage()
  try {
    await memberPage.goto(inviteLink)
    await memberPage.getByRole('link', { name: 'Create account to join' }).click()
    await memberPage.getByRole('link', { name: 'Sign up with SSO' }).click()
    await memberPage.locator('#login').fill(account.invitee)
    await memberPage.locator('#password').fill('ownscale')
    await memberPage.locator('#submit-login').click()
    await expect(memberPage.getByRole('link', { name: 'Machines' })).toBeVisible({
      timeout: 15_000,
    })

    // Users page: no "Invite member" button, no role dropdowns/Remove
    // buttons for other members. The Invitations nav link is hidden, and
    // the route itself redirects away.
    await memberPage.goto('/users')
    await expect(memberPage.getByRole('heading', { name: 'Users' })).toBeVisible()
    await expect(memberPage.getByRole('button', { name: 'Invite member' })).toBeHidden()
    await expect(memberPage.getByRole('combobox')).toHaveCount(0)
    await expect(memberPage.getByRole('button', { name: 'Remove' })).toHaveCount(0)
    await expect(memberPage.getByRole('link', { name: 'Invitations' })).toBeHidden()

    await memberPage.goto('/invitations')
    await expect(memberPage).toHaveURL(/\/machines$/)

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
  await confirmDialog(page, 'Remove member')
  await expect(memberRow).toBeHidden()
})
