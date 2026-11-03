// api client: thin wrapper around fetch() for the Ownscale console's JSON API.
// All calls are same-origin and rely on the session cookie set by the
// server-driven OAuth flow (see /auth/{provider}).

import type {
  PublicKeyCredentialCreationOptionsJSON,
  PublicKeyCredentialRequestOptionsJSON,
  RegistrationResponseJSON,
  AuthenticationResponseJSON,
} from '@simplewebauthn/browser'

export interface User {
  ID: string
  OrganizationID: string
  Provider: string
  ProviderUserID: string
  Email: string
  Name: string
  AvatarUrl: string
  CreatedAt: string
  UpdatedAt: string
  Role: string
}

// isPasskeyEmail reports whether email is the synthetic address a passkey
// account carries instead of a real one (see handlers.passkeyEmail), which
// shouldn't be shown to the user.
export function isPasskeyEmail(email: string): boolean {
  return email.endsWith('@passkey')
}

// displayName is how a user should be labelled in the UI: their name, or
// their email when they have no name (and it isn't the synthetic passkey one).
export function displayName(user: Pick<User, 'Name' | 'Email'>): string {
  if (user.Name) return user.Name
  return isPasskeyEmail(user.Email) ? '' : user.Email
}

export interface Organization {
  ID: string
  Domain: string
  CreatedAt: string
  // The state of the org's headscale: 'update_pending' while a settings save
  // restarts it, 'sleeping' when it was stopped while unused (it starts on
  // next use), 'unavailable' when it failed to start, stopped, or lost its
  // database.
  Status: 'active' | 'update_pending' | 'sleeping' | 'unavailable'
}

// AssignableUser is an organization member the caller may register machines
// and create keys for. id is the member's ownscale user ID.
export interface AssignableUser {
  id: string
  name: string
  email: string
}

export interface HeadscaleUser {
  id: string
  name: string
  displayName: string
  email: string
  createdAt: string
}

export interface Machine {
  id: string
  machineKey: string
  nodeKey: string
  ipAddresses: string[]
  name: string
  givenName: string
  user: HeadscaleUser | null
  lastSeen: string | null
  expiry: string | null
  // expired is whether expiry has passed by the server's clock.
  expired: boolean
  createdAt: string
  registerMethod: string
  online: boolean
  tags: string[] | null
  approvedRoutes: string[] | null
  availableRoutes: string[] | null
  subnetRoutes: string[] | null
  internal: boolean
  // canEdit is whether the caller may manage this machine.
  canEdit: boolean
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

// Dispatched on any 401 other than /api/session itself, so AuthProvider can
// clear the stale session and let the router redirect to /login instead of
// leaving the current page stuck showing a raw "unauthorized" error.
export const UNAUTHORIZED_EVENT = 'ownscale:unauthorized'

// errorMessage returns the message to show for err.
export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  // Spread init first: its own headers would otherwise replace the merged ones.
  const headers = new Headers(init?.body ? { 'Content-Type': 'application/json' } : undefined)
  new Headers(init?.headers).forEach((value, key) => headers.set(key, value))
  const res = await fetch(path, { ...init, credentials: 'same-origin', headers })
  if (res.status === 204) {
    return undefined as T
  }
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    if (res.status === 401 && path !== '/api/session') {
      window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
    }
    throw new ApiError(res.status, body?.error ?? res.statusText)
  }
  return body as T
}

export function getSession() {
  return request<{ user: User; organization: Organization | null; version: string }>('/api/session')
}

export interface AuthProviders {
  oidc: boolean
  google: boolean
  microsoft: boolean
  github: boolean
}

// getAuthProviders reports which upstream login providers are configured,
// so the login page can grey out buttons for ones that aren't (see
// handlers.AuthProviders). Public: called before the user is signed in.
export function getAuthProviders() {
  return request<AuthProviders>('/api/auth/providers')
}

export function logout() {
  return request<void>('/api/logout', { method: 'POST' })
}

export interface MachinesResponse {
  machines: Machine[]
  users: HeadscaleUser[]
  // The members the caller may register machines for.
  assignableUsers: AssignableUser[]
  total: number
  // False for members, who may only manage their own machines.
  canManage: boolean
}

// sshLogoutSettlesAt is when the browser SSH page's last logout should have
// reached headscale, which then deletes its ephemeral node. The wasm
// client's logout is fire-and-forget, so listings wait until then instead of
// showing the node.
// This is needed to avoid showing the already deleted ephemeral node created
// by the browser ssh page when navigating away from it.
let sshLogoutSettlesAt = 0

// noteSSHLogout records that the browser SSH page just logged out.
export function noteSSHLogout() {
  sshLogoutSettlesAt = Date.now() + 1000
}

export async function listMachines(
  params: { q?: string; user?: string; status?: string },
  signal?: AbortSignal,
) {
  const wait = sshLogoutSettlesAt - Date.now()
  if (wait > 0) await new Promise((resolve) => setTimeout(resolve, wait))
  const qs = new URLSearchParams()
  if (params.q) qs.set('q', params.q)
  if (params.user) qs.set('user', params.user)
  if (params.status) qs.set('status', params.status)
  const suffix = qs.toString() ? `?${qs.toString()}` : ''
  return request<MachinesResponse>(`/api/machines${suffix}`, { signal })
}

export interface DeviceInfo {
  os: string
  clientVersion: string
  hostname: string
  updateAvailable: boolean
}

export interface MachineDetailResponse {
  machine: Machine
  deviceInfo: DeviceInfo
  // fullDomain is empty when MagicDNS is off.
  fullDomain: string
  // canManage is whether the caller may change the machine's tags and routes.
  canManage: boolean
}

export function getMachineByIP(ip: string, signal?: AbortSignal) {
  return request<MachineDetailResponse>(`/api/machines/by-ip/${encodeURIComponent(ip)}`, { signal })
}

export function registerMachine(memberId: string, key: string) {
  return request<{ machine: Machine }>('/api/machines/register', {
    method: 'POST',
    body: JSON.stringify({ memberId, key }),
  })
}

export function renameMachine(id: string, name: string) {
  return request<{ ok: boolean }>(`/api/machines/${id}/rename`, {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export function setMachineTags(id: string, tags: string[]) {
  return request<{ ok: boolean }>(`/api/machines/${id}/tags`, {
    method: 'POST',
    body: JSON.stringify({ tags }),
  })
}

// isExitRoute reports whether route is one of the two default routes a
// machine advertises to act as an exit node.
export function isExitRoute(route: string): boolean {
  return route === '0.0.0.0/0' || route === '::/0'
}

export type ExitNodeStatus = 'none' | 'awaiting' | 'approved'

// exitNodeStatus reports whether m advertises itself as an exit node and,
// if so, whether that has been approved.
export function exitNodeStatus(m: Machine): ExitNodeStatus {
  if (!(m.availableRoutes ?? []).some(isExitRoute)) return 'none'
  return (m.approvedRoutes ?? []).some(isExitRoute) ? 'approved' : 'awaiting'
}

export function setMachineRoutes(id: string, routes: string[]) {
  return request<{ ok: boolean }>(`/api/machines/${id}/routes`, {
    method: 'POST',
    body: JSON.stringify({ routes }),
  })
}

export function expireMachine(id: string) {
  return request<{ ok: boolean }>(`/api/machines/${id}/expire`, { method: 'POST' })
}

// setMachineExpiry disables a machine's key expiry, or re-enables it with
// expiry (an ISO timestamp) as the new date.
export function setMachineExpiry(
  id: string,
  change: { mode: 'disable' } | { mode: 'enable'; expiry: string },
) {
  return request<{ ok: boolean }>(`/api/machines/${id}/expire`, {
    method: 'POST',
    body: JSON.stringify(change),
  })
}

export function deleteMachine(id: string) {
  return request<{ ok: boolean }>(`/api/machines/${id}`, { method: 'DELETE' })
}

// SSHSession is everything the in-browser Tailscale client needs to join
// the tailnet and open a session to a machine (see handlers.CreateSSHSession).
export interface SSHSession {
  controlURL: string
  // Single use, short lived, and only ever held in memory.
  authKey: string
  // Lets the console delete the key once it's consumed, instead of
  // waiting out its short server-side TTL.
  authKeyId: string
  hostname: string
  target: string
  machineName: string
  loginName: string
}

export function createSSHSession(id: string) {
  return request<SSHSession>(`/api/machines/${id}/ssh-session`, { method: 'POST' })
}

// Roles mirror common tailnet-admin-tool conventions for UI parity; only
// owner/admin are actually enforced server-side (see handlers/roles.go).
export const ROLES = ['owner', 'admin', 'network_admin', 'it_admin', 'auditor', 'member'] as const
export type Role = (typeof ROLES)[number]

export interface Member {
  id: string
  name: string
  email: string
  role: Role
}

export interface MembersResponse {
  members: Member[]
  canManage: boolean
  callerId: string
  callerRole: Role
}

export function listMembers(signal?: AbortSignal) {
  return request<MembersResponse>('/api/members', { signal })
}

export function updateMemberRole(id: string, role: Role) {
  return request<{ ok: boolean }>(`/api/members/${id}/role`, {
    method: 'POST',
    body: JSON.stringify({ role }),
  })
}

export function removeMember(id: string) {
  return request<{ ok: boolean }>(`/api/members/${id}`, { method: 'DELETE' })
}

// deleteAccount deletes the caller's own account and ends their session.
export function deleteAccount() {
  return request<void>('/api/account', { method: 'DELETE' })
}

export function transferOwnership(memberId: string) {
  return request<{ ok: boolean }>('/api/members/transfer-ownership', {
    method: 'POST',
    body: JSON.stringify({ memberId }),
  })
}

export interface Invite {
  id: string
  email: string | null
  role: Role
  token: string
  createdAt: string
  expiresAt: string
}

export function listInvites(signal?: AbortSignal) {
  return request<{ invites: Invite[] }>('/api/invites', { signal })
}

export function createInvite(email: string, role: Role) {
  return request<{ invite: Invite }>('/api/invites', {
    method: 'POST',
    body: JSON.stringify({ email, role }),
  })
}

export function deleteInvite(id: string) {
  return request<{ ok: boolean }>(`/api/invites/${id}`, { method: 'DELETE' })
}

export interface InvitePreview {
  valid: boolean
  organizationName: string
  organizationDomain: string
  invitedBy: string
  role: Role
}

export function getInvitePreview(token: string, signal?: AbortSignal) {
  return request<InvitePreview>(`/api/invites/${token}`, { signal })
}

export interface AccessControlResponse {
  content: string
  canManage: boolean
}

export function getAccessControlPolicy(signal?: AbortSignal) {
  return request<AccessControlResponse>('/api/access-control', { signal })
}

// checkAccessControlPolicy validates content and runs its tests without
// saving; it rejects with the failing tests' details.
export function checkAccessControlPolicy(content: string) {
  return request<{ ok: boolean }>('/api/access-control/check', {
    method: 'POST',
    body: JSON.stringify({ content }),
  })
}

export function updateAccessControlPolicy(content: string) {
  return request<{ ok: boolean }>('/api/access-control', {
    method: 'PUT',
    body: JSON.stringify({ content }),
  })
}

export function formatHuJSON(content: string) {
  return request<{ content: string }>('/api/hujson/format', {
    method: 'POST',
    body: JSON.stringify({ content }),
  })
}

export interface AuthRestrictionsResponse {
  content: string
  canManage: boolean
}

export function getAuthRestrictions(signal?: AbortSignal) {
  return request<AuthRestrictionsResponse>('/api/auth-restrictions', { signal })
}

export function updateAuthRestrictions(content: string) {
  return request<{ ok: boolean }>('/api/auth-restrictions', {
    method: 'PUT',
    body: JSON.stringify({ content }),
  })
}

export interface DNSRecord {
  name: string
  type: string
  value: string
}

export interface Nameservers {
  global: string[]
  // Maps a domain to the nameservers used for it and its subdomains.
  split: Record<string, string[]>
}

export interface DNSConfig {
  baseDomain: string
  magicDns: boolean
  nameservers: Nameservers
  searchDomains: string[]
  extraRecords: DNSRecord[]
}

export interface DNSSettingsResponse {
  dns: DNSConfig
  canManage: boolean
  // Whether the server can issue node certificates; also needs Magic DNS.
  httpsCertificates: boolean
}

export function getDNSSettings(signal?: AbortSignal) {
  return request<DNSSettingsResponse>('/api/dns', { signal })
}

export function updateDNSSettings(dns: DNSConfig) {
  return request<{ ok: boolean }>('/api/dns', {
    method: 'PUT',
    body: JSON.stringify(dns),
  })
}

export interface PreAuthKey {
  id: string
  key: string
  user: HeadscaleUser | null
  reusable: boolean
  ephemeral: boolean
  used: boolean
  aclTags: string[] | null
  createdAt: string
  expiration: string
  // expired is whether expiration has passed by the server's clock.
  expired: boolean
}

export interface AuthKeysResponse {
  // Members only get their own keys.
  authKeys: PreAuthKey[]
  users: HeadscaleUser[]
  // The members the caller may create keys for.
  assignableUsers: AssignableUser[]
  canManage: boolean
}

export function listAuthKeys(signal?: AbortSignal) {
  return request<AuthKeysResponse>('/api/auth-keys', { signal })
}

export interface CreateAuthKeyParams {
  memberId?: string
  aclTags?: string[]
  expiryDays: number
  reusable: boolean
  ephemeral: boolean
}

export function createAuthKey(params: CreateAuthKeyParams) {
  return request<{ authKey: PreAuthKey }>('/api/auth-keys', {
    method: 'POST',
    body: JSON.stringify(params),
  })
}

export function expireAuthKey(id: string) {
  return request<{ ok: boolean }>(`/api/auth-keys/${id}/expire`, { method: 'POST' })
}

export function deleteAuthKey(id: string) {
  return request<{ ok: boolean }>(`/api/auth-keys/${id}`, { method: 'DELETE' })
}

export interface FunnelRoute {
  id: string
  label: string
  hostname: string
  targetNodeId: string
  targetNodeName: string
  targetNodeMissing: boolean
  targetPort: number
  createdAt: string
  createdBy?: string
}

export interface FunnelRoutesResponse {
  funnelRoutes: FunnelRoute[]
  hostnameSuffix: string
  canManage: boolean
}

export function listFunnelRoutes(signal?: AbortSignal) {
  return request<FunnelRoutesResponse>('/api/funnel', { signal })
}

export interface CreateFunnelRouteParams {
  label: string
  targetNodeId: string
  targetPort: number
}

export function createFunnelRoute(params: CreateFunnelRouteParams) {
  return request<{ funnelRoute: FunnelRoute }>('/api/funnel', {
    method: 'POST',
    body: JSON.stringify(params),
  })
}

export function deleteFunnelRoute(id: string) {
  return request<{ ok: boolean }>(`/api/funnel/${id}`, { method: 'DELETE' })
}

export interface Passkey {
  id: string
  name: string
  createdAt: string
  lastUsedAt: string | null
}

export function listPasskeys(signal?: AbortSignal) {
  return request<{ passkeys: Passkey[] }>('/api/passkeys', { signal })
}

export function deletePasskey(id: string) {
  return request<{ ok: boolean }>(`/api/passkeys/${id}`, { method: 'DELETE' })
}

// The passkey begin/finish endpoints use go-webauthn's JSON shapes (options
// wrapped in "publicKey"), which lib/webauthn.ts adapts for the browser.

export function passkeyRegisterBegin(username: string, next?: string) {
  return request<{ publicKey: PublicKeyCredentialCreationOptionsJSON }>(
    '/api/passkey/register/begin',
    { method: 'POST', body: JSON.stringify({ username, next }) },
  )
}

export function passkeyRegisterFinish(response: RegistrationResponseJSON) {
  return request<void>('/api/passkey/register/finish', {
    method: 'POST',
    body: JSON.stringify(response),
  })
}

export function passkeyLoginBegin(next?: string) {
  return request<{ publicKey: PublicKeyCredentialRequestOptionsJSON }>('/api/passkey/login/begin', {
    method: 'POST',
    body: JSON.stringify({ next }),
  })
}

export function passkeyLoginFinish(response: AuthenticationResponseJSON) {
  return request<void>('/api/passkey/login/finish', {
    method: 'POST',
    body: JSON.stringify(response),
  })
}

export function passkeyAddBegin(name: string) {
  return request<{ publicKey: PublicKeyCredentialCreationOptionsJSON }>(
    '/api/passkeys/register/begin',
    { method: 'POST', body: JSON.stringify({ name }) },
  )
}

export function passkeyAddFinish(response: RegistrationResponseJSON) {
  return request<{ passkey: Passkey }>('/api/passkeys/register/finish', {
    method: 'POST',
    body: JSON.stringify(response),
  })
}
