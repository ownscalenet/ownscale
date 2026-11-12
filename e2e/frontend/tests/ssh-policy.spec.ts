import { expect, test } from './support/account'

// A policy whose "ssh" section lets users Tailscale SSH into their own
// machines. dst is the only field varied below.
function sshPolicy(dst: string): string {
  return `{
  "acls": [
    {"action": "accept", "src": ["*"], "dst": ["*:*"]},
  ],
  "ssh": [
    {
      "action": "accept",
      "src": ["autogroup:member"],
      "dst": [${JSON.stringify(dst)}],
      "users": ["autogroup:nonroot", "root"],
    },
  ],
}
`
}

test('an ssh section can be saved in the policy, and invalid ssh rules are rejected', async ({ page }) => {
  await page.goto('/access-control')
  await expect(page.getByRole('heading', { name: 'Access Control' })).toBeVisible()

  const editor = page.locator('textarea')
  const save = page.getByRole('button', { name: 'Save' })
  const saved = page.getByText('Policy saved.')
  const original = await editor.inputValue()

  try {
    await editor.fill(sshPolicy('autogroup:self'))
    await save.click()
    await expect(saved).toBeVisible()

    // Reloading shows what headscale actually stored.
    await page.reload()
    await expect(editor).toHaveValue(/"ssh"/)
    await expect(editor).toHaveValue(/autogroup:self/)

    // headscale rejects "*" as an SSH destination; the stored policy stays.
    await editor.fill(sshPolicy('*'))
    await save.click()
    await expect(page.locator('.alert-error')).toContainText('invalid dst "*"')
    await page.reload()
    await expect(editor).toHaveValue(/autogroup:self/)
  } finally {
    // Save is disabled while the editor matches what's stored.
    await page.reload()
    if ((await editor.inputValue()) !== original) {
      await editor.fill(original)
      await save.click()
      await expect(saved).toBeVisible()
    }
  }
})
