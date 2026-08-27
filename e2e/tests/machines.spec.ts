import { test, expect } from '@playwright/test'

// No real Tailscale client is registered against the e2e organization, so
// this only covers the Machines page's own rendering, filter controls, and
// empty state -- not per-machine actions (rename/tag/routes/expire/delete),
// which need an existing machine row. See e2e/README.md.
test('renders the machines page with filters and an empty state', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (err) => errors.push(err.message))
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(msg.text())
  })

  await page.goto('/machines')

  await expect(page.getByRole('heading', { name: 'Machines' })).toBeVisible()
  await expect(page.getByPlaceholder('Search by name or IP…')).toBeVisible()

  // No headscale user filter should still render "All users" / "Any status".
  await expect(page.getByRole('combobox').first()).toContainText('All users')

  // Exercise the search filter: typing updates the URL's ?q= param and
  // doesn't throw, even with zero matching machines.
  await page.getByPlaceholder('Search by name or IP…').fill('nonexistent-machine')
  await expect(page).toHaveURL(/[?&]q=nonexistent-machine/)
  await page.getByRole('button', { name: 'Clear' }).click()
  await expect(page).not.toHaveURL(/[?&]q=/)

  expect(errors, `unexpected console/page errors: ${errors.join('\n')}`).toEqual([])
})
