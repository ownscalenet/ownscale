import { expect, test } from './support/account'

test('editing and saving the ACL policy round-trips through Format and Save', async ({ page }) => {
  await page.goto('/access-control')
  await expect(page.getByRole('heading', { name: 'Access Control' })).toBeVisible()

  const editor = page.locator('textarea')
  const original = await editor.inputValue()
  const marker = `// e2e-${Date.now()}`

  await editor.fill(`${original}\n${marker}\n`)
  await expect(page.getByRole('button', { name: 'Save' })).toBeEnabled()

  await page.getByRole('button', { name: 'Format' }).click()
  await expect(editor).toContainText(marker)

  const savedBanner = page.getByText('Policy saved.')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(savedBanner).toBeVisible()

  // Restore the original policy so repeat local runs (and other specs)
  // aren't left with an ever-growing pile of e2e markers.
  await editor.fill(original)
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(savedBanner).toBeVisible()
})

test('Run tests and Save show failing policy tests', async ({ page }) => {
  await page.goto('/access-control')
  const editor = page.locator('textarea')
  await expect(editor).not.toHaveValue('')
  const original = await editor.inputValue()
  const policy = (assertion: string) =>
    `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],"tests":[{"src":"10.0.0.1",${assertion}}]}`

  await editor.fill(policy('"accept":["10.0.0.2:22"]'))
  await page.getByRole('button', { name: 'Run tests' }).click()
  const failure = page.getByRole('alert').filter({ hasText: 'expected ALLOWED, got DENIED' })
  await expect(failure).toBeVisible()
  await expect(failure).toContainText('10.0.0.1 -> 10.0.0.2:22')

  // A failing test blocks the save and is shown the same way.
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(failure).toBeVisible()
  await expect(page.getByText('Policy saved.')).toHaveCount(0)

  await editor.fill(policy('"deny":["10.0.0.2:22"]'))
  await expect(failure).toHaveCount(0)
  await page.getByRole('button', { name: 'Run tests' }).click()
  await expect(page.getByText('Policy is valid and all its tests pass.')).toBeVisible()

  // Running tests never saves, so the stored policy is still the original.
  await page.reload()
  await expect(editor).toHaveValue(original)
})
