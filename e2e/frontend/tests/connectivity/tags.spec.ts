import { test, expect } from './fixtures'

test('setting a machine\'s ACL tags succeeds with or without the tag: prefix, even for brand-new tags', async ({ harness }) => {
  const org = harness.orgs[0]
  const original = await org.policy()
  await harness.recovery.snapshot(org.org, original)

  const endpoint = await harness.enroll(org, 0)
  const { page } = org
  await page.goto('/machines')
  const row = page.getByRole('row').filter({ has: page.getByText(endpoint.info.hostname, { exact: true }) })
  const dialog = page.locator('dialog.modal[open]')

  await test.step('a tag typed without the tag: prefix is normalized and accepted', async () => {
    await row.getByRole('button', { name: '⋯', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Edit ACL tags', exact: true }).click()
    await dialog.locator('input[type="text"]').fill('e2etagnoprefix')
    const responsePromise = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/tags') && r.request().method() === 'POST')
    await dialog.getByRole('button', { name: 'Save', exact: true }).click()
    const response = await responsePromise
    expect(response.ok(), `expected tag save to succeed, got HTTP ${response.status()}`).toBeTruthy()
    await expect(dialog).toBeHidden()
    await expect(row.getByText('tag:e2etagnoprefix', { exact: true })).toBeVisible()
  })

  await test.step('a brand-new tag with the tag: prefix, never declared in this org\'s ACL policy before, is auto-provisioned and accepted', async () => {
    await row.getByRole('button', { name: '⋯', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Edit ACL tags', exact: true }).click()
    await dialog.locator('input[type="text"]').fill('tag:e2etagnoprefix, tag:e2etagwithprefix')
    const responsePromise = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/tags') && r.request().method() === 'POST')
    await dialog.getByRole('button', { name: 'Save', exact: true }).click()
    const response = await responsePromise
    expect(response.ok(), `expected tag save to succeed, got HTTP ${response.status()}`).toBeTruthy()
    await expect(dialog).toBeHidden()
    await expect(row.getByText('tag:e2etagwithprefix', { exact: true })).toBeVisible()

    const policy = await org.policy()
    const withoutComments = policy.replace(/\/\/.*$/gm, '')
    const withoutTrailingCommas = withoutComments.replace(/,(\s*[}\]])/g, '$1')
    const parsed = JSON.parse(withoutTrailingCommas) as { tagOwners?: Record<string, string[]> }
    expect(parsed.tagOwners?.['tag:e2etagwithprefix'], 'ACL policy must declare the new tag in tagOwners').toBeTruthy()
  })

  await org.savePolicy(original)
  await harness.recovery.restored(org.org)
})
