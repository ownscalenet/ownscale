import { expect, test } from './support/account'
import { retryUntil } from './support/retry'

test('editing and saving the OIDC allowlist round-trips through Format and Save', async ({
  page,
}) => {
  test.setTimeout(120_000)
  await page.goto('/auth-restrictions')
  await expect(page.getByRole('heading', { name: 'Authentication Restrictions' })).toBeVisible()

  const editor = page.locator('textarea')
  const original = await editor.inputValue()
  const marker = `// e2e-${Date.now()}`
  const withMarker = `${original}\n${marker}\n`

  await editor.fill(withMarker)
  await expect(page.getByRole('button', { name: 'Save' })).toBeEnabled()
  await page.getByRole('button', { name: 'Format' }).click()
  await expect(editor).toContainText(marker)

  // The save is verified against the persisted content after a reload
  // (not the transient "saved" banner) -- see support/retry.ts for why.
  await retryUntil(
    page,
    async () => {
      await editor.fill(withMarker)
      await page.getByRole('button', { name: 'Save' }).click()
    },
    async () => {
      await expect(editor).toContainText(marker, { timeout: 8_000 })
    },
  )

  // Restore the original allowlist so repeat local runs (and other specs)
  // aren't left with an ever-growing pile of e2e markers.
  await retryUntil(
    page,
    async () => {
      await editor.fill(original)
      await page.getByRole('button', { name: 'Save' }).click()
    },
    async () => {
      await expect(editor).not.toContainText(marker, { timeout: 8_000 })
    },
  )
})
