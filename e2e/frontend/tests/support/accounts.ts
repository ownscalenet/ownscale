// accounts are the e2e Dex identities (etc/dex/config.yaml). Each owns the
// org it's named after (OWNSCALE_FIXED_SUBDOMAINS), so suites never change
// each other's org-wide state. Workers each have an invitee: a Dex user
// that's never registered, because invites only apply to brand-new users.
export const accounts = [
  { name: 'aaaa', role: 'connectivity' },
  { name: 'bbbb', role: 'connectivity' },
  { name: 'cccc', role: 'connectivity' },
  { name: 'dddd', role: 'serve' },
  { name: 'eeee', role: 'worker', invitee: 'invitee0@ownscale.test' },
  { name: 'ffff', role: 'worker', invitee: 'invitee1@ownscale.test' },
  { name: 'gggg', role: 'worker', invitee: 'invitee2@ownscale.test' },
  { name: 'hhhh', role: 'ssh' },
  { name: 'iiii', role: 'spare' },
  { name: 'jjjj', role: 'spare' },
] as const

export type Role = typeof accounts[number]['role']

// namesOf lists the accounts with role, e.g. the frontend workers by parallel index.
export function namesOf(role: Role): string[] {
  return accounts.filter(a => a.role === role).map(a => a.name)
}

export function emailOf(name: string): string {
  return `${name}@ownscale.test`
}

export function storageStateOf(name: string): string {
  return `tests/.auth/${name}.json`
}

// setupProject is the "setup" project that signs in only names.
export function setupProject(names: string[]) {
  return { name: 'setup', testMatch: /global\.setup\.ts/, grep: new RegExp(`\\b(${names.join('|')}) signs in\\b`) }
}
