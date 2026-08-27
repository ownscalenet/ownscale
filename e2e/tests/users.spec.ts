import { test, expect } from '@playwright/test'
import { ADMIN_EMAIL } from '../lib/dex'

test('lists the admin as owner, and hides headscale users entirely', async ({ page }) => {
  await page.goto('/users')

  await expect(page.getByRole('heading', { name: 'Users', exact: true })).toBeVisible()

  const membersTable = page.locator('table').first()
  await expect(membersTable.getByText(ADMIN_EMAIL)).toBeVisible()
  await expect(membersTable.getByText('Owner', { exact: true })).toBeVisible()

  // Headscale users are an internal implementation detail (auto-linked by
  // email) -- there must be no way for an end-user to see or manage them.
  await expect(page.getByRole('heading', { name: 'Headscale users' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Create user' })).toHaveCount(0)
  await expect(page.getByRole('columnheader', { name: 'Linked headscale user' })).toHaveCount(0)
  await expect(page.getByRole('columnheader', { name: 'Linked member' })).toHaveCount(0)
})
