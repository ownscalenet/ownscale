import { test, expect } from '@playwright/test'

test('formats, diffs, and saves the access control policy', async ({ page }) => {
  await page.goto('/access-control')

  await expect(page.getByRole('heading', { name: 'Access Control' })).toBeVisible()

  const textarea = page.locator('textarea')
  await expect(textarea).not.toHaveValue('')

  const diffButton = page.getByRole('button', { name: 'View diff' })
  const saveButton = page.getByRole('button', { name: 'Save' })

  // Nothing has been edited yet: both actions should be disabled.
  await expect(diffButton).toBeVisible()
  await expect(diffButton).toBeDisabled()
  await expect(saveButton).toBeDisabled()

  // Edit the policy: strip comment lines (collapsing them would turn the
  // rest of the document into a trailing HuJSON comment) then compact the
  // remaining JSON onto one squished line so Format has visible work to do.
  const original = await textarea.inputValue()
  const withoutComments = original
    .split('\n')
    .filter((line) => !line.trim().startsWith('//'))
    .join('\n')
  const edited = withoutComments.replace(/\n\s*/g, ' ')
  await textarea.fill(edited)
  await expect(diffButton).toBeEnabled()
  await expect(saveButton).toBeEnabled()

  // Format: pretty-prints back into a multi-line document.
  await page.getByRole('button', { name: 'Format' }).click()
  await expect(async () => {
    const value = await textarea.inputValue()
    expect(value.split('\n').length).toBeGreaterThan(1)
  }).toPass()

  // View diff: opens a modal showing at least one changed line.
  await diffButton.click()
  const dialog = page.locator('dialog', { hasText: 'Diff since last save' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText('No changes.')).toBeHidden()
  await dialog.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(dialog).toBeHidden()

  // Save: persists and restarts headscale via the real Docker API. That
  // container restart changes the host's network interfaces, which Chrome
  // (on Linux) reacts to by cancelling the in-flight save fetch with
  // ERR_NETWORK_CHANGED -- a real, consistently-reproducible quirk of this
  // environment, not a flake to retry (retrying would just trigger another
  // restart and another cancellation). The backend still completes the
  // write server-side even though the client's fetch was aborted, so
  // tolerate either outcome and confirm success via reload/persistence.
  const formatted = await textarea.inputValue()
  await saveButton.click()
  await expect(page.getByText('Policy saved. Headscale is restarting to apply it.').or(page.getByText('Failed to fetch'))).toBeVisible()

  await expect(async () => {
    await page.reload()
    await expect(textarea).toHaveValue(formatted)
  }).toPass({ timeout: 30_000 })
  await expect(page.getByRole('button', { name: 'View diff' })).toBeDisabled()
})
