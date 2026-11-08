import { baseDomain } from '../connectivity/environment'

/**
 * extraTestEnabled reports whether EXTRA_TESTS (e.g. "example.com:serve,other.org:serve")
 * opts test name in for the stack under test. Extra tests are opt-in because they cost
 * something on that stack, like a Let's Encrypt certificate.
 */
export function extraTestEnabled(name: string, env = process.env.EXTRA_TESTS ?? ''): boolean {
  return env.split(',').map(e => e.trim()).filter(Boolean).some(entry => {
    const [domain, test, ...rest] = entry.split(':').map(p => p.trim())
    if (!domain || !test || rest.length > 0)
      throw new Error(`EXTRA_TESTS entry "${entry}" is not <domain>:<test>`)
    return domain === baseDomain && test === name
  })
}
