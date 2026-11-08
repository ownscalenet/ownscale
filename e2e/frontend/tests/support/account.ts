import { readFileSync } from 'node:fs'
import { test as base, expect, type BrowserContextOptions, type Page } from '@playwright/test'
import { baseDomain } from '../connectivity/environment'

// Each frontend worker signs in as its own Dex identity, which owns its own
// organization, so workers never change each other's org-wide state
// (policy, DNS, invites, members, ...). The ssh and serve suites have their
// own accounts too. global.setup.ts signs them in and records their orgs in
// accountsFile.

// workerAccounts are the frontend workers' identities, by parallel index.
// Each has its own invitee: a Dex user that's never registered beforehand,
// because invites only apply to brand-new users.
export const workerAccounts = [
  { name: 'worker0', invitee: 'invitee0@ownscale.test' },
  { name: 'worker1', invitee: 'invitee1@ownscale.test' },
  { name: 'worker2', invitee: 'invitee2@ownscale.test' },
] as const

// accountNames are signed in by global.setup.ts, in this order, so on a
// fresh stack they get the fixed subdomains after admin/user1/user2/passkey.
export const accountNames = [...workerAccounts.map(a => a.name), 'ssh', 'serve']

export const accountsFile = 'tests/.auth/accounts.json'

// passkeyCredentialFile is per base domain: a credential minted on another
// stack fails to sign in, and setup would then register another passkey org.
export const passkeyCredentialFile = `tests/.auth/passkey-credential.${baseDomain}.json`

export function emailOf(name: string): string {
  return `${name}@ownscale.test`
}

export function storageStateOf(name: string): string {
  return `tests/.auth/${name}.json`
}

// Account is the signed-in identity a test runs as.
export interface Account {
  name: string
  email: string
  // org is the organization's subdomain, e.g. "eeee".
  org: string
  storageState: string
  // invitee is a never-registered identity to invite into org; only
  // frontend workers have one.
  invitee: string
}

// signIn signs page in through Dex as email, starting from /login.
export async function signIn(page: Page, email: string): Promise<void> {
  await page.goto('/login')
  await page.getByRole('link', { name: 'Sign in with SSO' }).click()
  await page.locator('#login').fill(email)
  await page.locator('#password').fill('ownscale')
  await page.locator('#submit-login').click()
  await expect(page.getByRole('button', { name: 'Logout' })).toBeVisible({ timeout: 30_000 })
}

// defaultAuthRestrictions is the empty OIDC login allowlist (orgs.go's
// defaultAuthRestrictions), admitting any upstream login.
const defaultAuthRestrictions = '{"domains": [], "groups": [], "users": []}'

// resetAuthRestrictions resets page's signed-in org's OIDC login allowlist
// back to the default (empty) policy. Runs unconditionally during setup, so
// a run interrupted mid-test (e.g. settings-apply.spec.ts's own restore
// never completing) can't leave a later run stuck with a stale restrictive
// allowlist, since orgs are never reset between runs on a persistent stack.
export async function resetAuthRestrictions(page: Page): Promise<void> {
  let lastStatus = 0
  for (let attempt = 1; attempt <= 3; attempt++) {
    const response = await page.request.put('/api/auth-restrictions', { data: { content: defaultAuthRestrictions } })
    if (response.ok()) return
    lastStatus = response.status()
    await new Promise(resolve => setTimeout(resolve, 2_000))
  }
  throw new Error(`resetAuthRestrictions: /api/auth-restrictions PUT failed after retries, last HTTP ${lastStatus}`)
}

// removeInvitee removes invitee, and passkey invitees (see
// members-invites.spec.ts), from page's signed-in org if a previous run left
// them there. Invites only apply to brand-new accounts, so a leftover
// invitee would join the next test's org with their old role instead.
export async function removeInvitee(page: Page, invitee: string): Promise<void> {
  const response = await page.request.get('/api/members')
  if (!response.ok()) throw new Error(`removeInvitee: /api/members GET failed with HTTP ${response.status()}`)
  const { members } = (await response.json()) as { members: { id: string; email: string }[] }
  for (const m of members.filter(m => m.email === invitee || /^invitee\d+@passkey$/.test(m.email))) {
    const removed = await page.request.delete(`/api/members/${m.id}`)
    if (!removed.ok()) throw new Error(`removeInvitee: removing ${m.email} failed with HTTP ${removed.status()}`)
  }
}

function loadAccount(name: string): Account {
  let orgs: Record<string, string>
  try {
    orgs = JSON.parse(readFileSync(accountsFile, 'utf-8'))
  } catch {
    throw new Error(`${accountsFile} is missing: run the setup project first (make e2e-setup)`)
  }
  const org = orgs[name]
  if (!org) throw new Error(`${accountsFile} has no org for ${name}: run the setup project again`)
  const worker = workerAccounts.find(a => a.name === name)
  return { name, email: emailOf(name), org, storageState: storageStateOf(name), invitee: worker?.invitee ?? '' }
}

// test runs each spec as its worker's account: worker<parallelIndex>, or
// accountName when a config sets it (the ssh suite).
export const test = base.extend<{ storageState: BrowserContextOptions['storageState'] }, { account: Account; accountName: string | undefined }>({
  accountName: [undefined, { option: true, scope: 'worker' }],
  account: [async ({ accountName }, use, workerInfo) => {
    const name = accountName ?? workerAccounts[workerInfo.parallelIndex]?.name
    if (!name) throw new Error(`no account for worker ${workerInfo.parallelIndex}: add one to workerAccounts`)
    await use(loadAccount(name))
  }, { scope: 'worker' }],
  storageState: async ({ account }, use) => use(account.storageState),
})

export { expect }
