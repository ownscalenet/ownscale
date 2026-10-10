import type { Page } from '@playwright/test'

// confirmDialog accepts the console's confirmation dialog (ConfirmProvider)
// by clicking its confirmLabel button, after typing the text it asks for.
export async function confirmDialog(
  page: Page,
  confirmLabel: string,
  typed?: string,
): Promise<void> {
  if (typed !== undefined) await page.getByRole('dialog').getByRole('textbox').fill(typed)
  await page.getByRole('dialog').getByRole('button', { name: confirmLabel, exact: true }).click()
}
