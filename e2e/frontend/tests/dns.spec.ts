import { expect, test } from '@playwright/test'
import { retryUntil } from './support/retry'

test('adding and removing a nameserver, DNS record, and search domain, and toggling Magic DNS', async ({
  page,
}) => {
  // This test issues 8 back-to-back saves, each of which restarts aaaa's
  // headscale container and can need retries (see support/retry.ts) --
  // comfortably longer than Playwright's 30s default is needed.
  test.setTimeout(240_000)
  await page.goto('/dns')
  await expect(page.getByRole('heading', { name: 'DNS', exact: true })).toBeVisible()

  const nameserver = `10.${Date.now() % 200}.0.1`
  // Nameserver/search-domain chips render as a single <span> containing
  // both the value text and a "Remove" button, so getByText(..., {exact:
  // true}) never matches (the span's full text includes the button's
  // "✕") -- match on the badge container instead.
  const nameserverBadge = page.locator('.badge', { hasText: nameserver })
  await retryUntil(
    page,
    async () => {
      if (await nameserverBadge.isVisible()) return
      await page.getByPlaceholder('1.1.1.1').fill(nameserver)
      await page.getByRole('button', { name: 'Add' }).first().click()
    },
    async () => {
      await expect(nameserverBadge).toBeVisible({ timeout: 8_000 })
    },
  )

  await retryUntil(
    page,
    async () => {
      if (!(await nameserverBadge.isVisible())) return
      await page.getByRole('button', { name: `Remove ${nameserver}` }).click()
    },
    async () => {
      await expect(nameserverBadge).toBeHidden({ timeout: 8_000 })
    },
  )

  const recordName = `e2e-${Date.now()}.example.com`
  const recordRow = page.getByRole('row').filter({ hasText: recordName })
  await retryUntil(
    page,
    async () => {
      if (await recordRow.isVisible()) return
      await page.getByPlaceholder('Name (e.g. server.example.com)').fill(recordName)
      await page.getByPlaceholder('Value (e.g. 10.0.0.1)').fill('10.0.0.42')
      await page.locator('.card', { hasText: 'DNS Records' }).getByRole('button', { name: 'Add' }).click()
    },
    async () => {
      await expect(recordRow).toBeVisible({ timeout: 8_000 })
    },
  )

  await retryUntil(
    page,
    async () => {
      if (!(await recordRow.isVisible())) return
      await recordRow.getByRole('button', { name: 'Remove' }).click()
    },
    async () => {
      await expect(recordRow).toBeHidden({ timeout: 8_000 })
    },
  )

  const searchDomain = `internal-${Date.now()}.example.com`
  const searchDomainBadge = page.locator('.badge', { hasText: searchDomain })
  await retryUntil(
    page,
    async () => {
      if (await searchDomainBadge.isVisible()) return
      await page.getByPlaceholder('internal.example.com').fill(searchDomain)
      await page
        .locator('.card', { hasText: 'Search Domains' })
        .getByRole('button', { name: 'Add' })
        .click()
    },
    async () => {
      await expect(searchDomainBadge).toBeVisible({ timeout: 8_000 })
    },
  )

  await retryUntil(
    page,
    async () => {
      if (!(await searchDomainBadge.isVisible())) return
      await page.getByRole('button', { name: `Remove ${searchDomain}` }).click()
    },
    async () => {
      await expect(searchDomainBadge).toBeHidden({ timeout: 8_000 })
    },
  )

  const magicDnsToggle = page.locator('input[type="checkbox"].toggle')
  const wasEnabled = await magicDnsToggle.isChecked({ timeout: 8_000 })
  const flippedLabel = page.getByText(wasEnabled ? 'Disabled' : 'Enabled', { exact: true })
  await retryUntil(
    page,
    async () => {
      if ((await magicDnsToggle.isChecked({ timeout: 8_000 })) !== wasEnabled) return
      await magicDnsToggle.click()
    },
    async () => {
      await expect(flippedLabel).toBeVisible({ timeout: 8_000 })
    },
  )

  // Restore the original Magic DNS state so repeat runs/other specs aren't
  // left with it flipped.
  const restoredLabel = page.getByText(wasEnabled ? 'Enabled' : 'Disabled', { exact: true })
  await retryUntil(
    page,
    async () => {
      if ((await magicDnsToggle.isChecked({ timeout: 8_000 })) === wasEnabled) return
      await magicDnsToggle.click()
    },
    async () => {
      await expect(restoredLabel).toBeVisible({ timeout: 8_000 })
    },
  )
})
