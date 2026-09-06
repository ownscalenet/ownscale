import { expect, test } from '@playwright/test'

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

  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Policy saved. Headscale is restarting to apply it.')).toBeVisible()

  // Restore the original policy so repeat local runs (and other specs)
  // aren't left with an ever-growing pile of e2e markers.
  await editor.fill(original)
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Policy saved. Headscale is restarting to apply it.')).toBeVisible()
})
