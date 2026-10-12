import { expect, test } from './support/account'
import { registerRealMachine, teardownRealMachine } from './support/realMachine'

test('a machine page opens from the list by IPv4 and shows its details', async ({ page, account }) => {
  test.setTimeout(120_000)
  const machine = await registerRealMachine(page, account.org)
  try {
    await page.goto('/machines?q=mlc')
    await page.getByRole('row').filter({ hasText: machine.hostname }).getByRole('link', { name: machine.hostname }).click()

    await expect(page).toHaveURL(/\/machines\/100\.\d+\.\d+\.\d+$/)
    await expect(page.getByRole('heading', { name: machine.hostname, exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Machine details' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Addresses' })).toBeVisible()
    const ip = new URL(page.url()).pathname.split('/').pop()!
    await expect(page.getByText(ip, { exact: true }).first()).toBeVisible()
    await expect(page.getByText('Connected').first()).toBeVisible()

    await expect(page.getByRole('link', { name: 'SSH' })).toHaveAttribute('href', /\/machines\/\d+\/ssh$/)

    // The test machine registers without an expiry.
    await expect(page.getByText('Never expires')).toBeVisible()
    await page.getByRole('button', { name: 'Enable key expiry' }).click()
    await page.getByRole('button', { name: 'Enable expiry' }).click()
    await expect(page.getByRole('button', { name: 'Disable key expiry' })).toBeVisible()
    await expect(page.getByText('Never expires')).toHaveCount(0)

    await page.getByRole('button', { name: 'Disable key expiry' }).click()
    await page.getByRole('button', { name: 'Disable expiry' }).click()
    await expect(page.getByText('Never expires')).toBeVisible()

    await page.goto('/machines/100.64.255.255')
    await expect(page.getByText('Machine not found')).toBeVisible()
  } finally {
    await teardownRealMachine(machine)
  }
})
