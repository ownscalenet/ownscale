import { expect, test } from './support/account'

test('visiting an unknown route while signed in shows the not-found page', async ({ page }) => {
  await page.goto('/this-route-does-not-exist')
  await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible()
  await expect(page.getByText("The page you\u2019re looking for doesn\u2019t exist.")).toBeVisible()

  await page.getByRole('link', { name: 'Go home' }).click()
  await expect(page.getByRole('heading', { name: 'Page not found' })).toBeHidden()
})

test('visiting an unknown route while signed out shows the not-found page', async ({ browser }) => {
  const context = await browser.newContext({
    storageState: { cookies: [], origins: [] },
    ignoreHTTPSErrors: true,
  })
  try {
    const page = await context.newPage()
    await page.goto('/this-route-does-not-exist')
    await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Go home' })).toBeVisible()
  } finally {
    await context.close()
  }
})
