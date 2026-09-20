import { expect, test } from '@playwright/test'

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
  const dialog = page.locator('dialog.modal[open]')
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
}) => {
  await page.goto('/users')
  await expect(page.getByRole('heading', { name: 'Users' })).toBeVisible()

  await page.getByRole('button', { name: 'Invite member' }).click()
  const dialog = page.locator('dialog.modal[open]')
  await dialog.getByRole('combobox').selectOption('admin')
  await dialog.getByRole('button', { name: 'Create invite link' }).click()
  await expect(dialog.getByText(/expires in 7 days/i)).toBeVisible()
  const inviteLink = await dialog.getByRole('textbox').inputValue()
  await dialog.getByRole('button', { name: 'Done' }).click()

  const inviteeContext = await browser.newContext({ storageState: { cookies: [], origins: [] } })
  try {
    const inviteePage = await inviteeContext.newPage()
    await inviteePage.goto(inviteLink)
    await expect(inviteePage).toHaveURL(/\/login\?next=/)

    await inviteePage.getByRole('link', { name: 'Sign in with SSO' }).click()
    await inviteePage.locator('#login').fill('user3@ownscale.test')
    await inviteePage.locator('#password').fill('ownscale')
    await inviteePage.locator('#submit-login').click()

    await expect(inviteePage.getByRole('link', { name: 'Machines' })).toBeVisible({
      timeout: 15_000,
    })
  } finally {
    await inviteeContext.close()
  }

  await page.reload()
  const memberRow = page.getByRole('row').filter({ hasText: 'user3@ownscale.test' })
  await expect(memberRow).toBeVisible()
  await expect(memberRow.getByRole('combobox')).toHaveValue('admin')

  await expect(memberRow.getByRole('button', { name: 'Make owner' })).toBeVisible()

  await memberRow.getByRole('combobox').selectOption('auditor')
  await expect(memberRow.getByRole('combobox')).toHaveValue('auditor')

  page.once('dialog', (d) => void d.accept())
  await memberRow.getByRole('button', { name: 'Remove' }).click()
  await expect(memberRow).toBeHidden()
})
