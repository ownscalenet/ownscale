import { test, expect } from '@playwright/test'
import { baseDomain } from './environment'
import { identities, poll, validateSession } from './ui'

function session(identity: typeof identities[number]): Parameters<typeof validateSession>[0] {
  return {
    user: {
      ID: 'test', Email: identity.email, Name: identity.name, Role: 'owner',
      OrganizationID: 'org',
      Provider: 'oidc', ProviderUserID: 'fixture', AvatarUrl: '', CreatedAt: '', UpdatedAt: '',
    },
    organization: { Domain: `${identity.org}.${baseDomain}`, Status: 'active' },
  }
}

for (const identity of identities) {
  test(`${identity.name} requires its exact agreed organization`, () => {
    expect(validateSession(session(identity), identity)).toBe(true)
    const wrong = session(identity)
    wrong.organization!.Domain = `other.${baseDomain}`
    expect(() => validateSession(wrong, identity)).toThrow('unexpected registration assignment')
  })
}

for (const example of [
  { name: 'wrong email', change: (s: ReturnType<typeof session>) => { s.user.Email = 'unexpected@example.test' }, error: 'unexpected signed-in user' },
  { name: 'unprivileged role', change: (s: ReturnType<typeof session>) => { s.user.Role = 'member' }, error: 'owner/admin permission required' },
  { name: 'missing org', change: (s: ReturnType<typeof session>) => { s.organization = null }, error: 'no assigned organization' },
  { name: 'unknown status', change: (s: ReturnType<typeof session>) => { s.organization!.Status = 'unknown' }, error: 'unknown organization status' },
]) {
  test(`readiness rejects ${example.name} immediately`, () => {
    const value = session(identities[0])
    example.change(value)
    expect(() => validateSession(value, identities[0])).toThrow(example.error)
  })
}

test('a restarting organization waits, and manager readiness is accepted', () => {
  const value = session(identities[0])
  value.organization!.Status = 'update_pending'
  expect(validateSession(value, identities[0])).toBe(false)
  value.organization!.Status = 'active'
  value.user.Role = 'admin'
  expect(validateSession(value, identities[0])).toBe(true)
})

test('poll returns observations but never retries an unexpected failure', async () => {
  expect(await poll('ready', 100, async () => 'ready')).toBe('ready')
  await expect(poll('authorization', 1000, async () => { throw new Error('forbidden') })).rejects.toThrow('forbidden')
  await expect(poll('provisioning', 5, async () => undefined)).rejects.toThrow('last state: waiting for convergence')
})
