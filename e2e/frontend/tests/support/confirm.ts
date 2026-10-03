import type { Page } from '@playwright/test'

// confirmDialog accepts the console's confirmation dialog (ConfirmProvider)
// by clicking its confirmLabel button.
export async function confirmDialog(page: Page, confirmLabel: string): Promise<void> {
  await page.getByRole('dialog').getByRole('button', { name: confirmLabel, exact: true }).click()
}
