import { expect, type Page } from '@playwright/test'

// getPolicy returns the signed-in org's ACL policy (HuJSON).
export async function getPolicy(page: Page): Promise<string> {
  const res = await page.request.get('/api/access-control')
  expect(res.ok(), await res.text()).toBe(true)
  return ((await res.json()) as { content: string }).content
}

// putPolicy replaces the signed-in org's ACL policy with content.
export async function putPolicy(page: Page, content: string): Promise<void> {
  const res = await page.request.put('/api/access-control', { data: { content } })
  expect(res.ok(), await res.text()).toBe(true)
}
