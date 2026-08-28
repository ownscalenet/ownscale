// api client: thin wrapper around fetch() for the Ownscale console's JSON API.
// All calls are same-origin and rely on the session cookie set by the
// server-driven OAuth flow (see /auth/{provider}).

export interface User {
  ID: string
  OrganizationID: { String: string; Valid: boolean }
  Provider: string
  ProviderUserID: string
  Email: string
  Name: string
  AvatarUrl: string
  CreatedAt: string
  UpdatedAt: string
  Role: string
  HeadscaleUserID: { String: string; Valid: boolean }
}

export interface Organization {
  ID: string
  Subdomain: string
  CreatedAt: string
  Status: 'pending' | 'active' | 'failed'
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
  createdAt: string
  registerMethod: string
  online: boolean
  tags: string[] | null
  approvedRoutes: string[] | null
  availableRoutes: string[] | null
  subnetRoutes: string[] | null
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
    ...init,
  })
  if (res.status === 204) {
    return undefined as T
  }
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, body?.error ?? res.statusText)
  }
  return body as T
}

export function getSession() {
  return request<{ user: User; organization: Organization | null }>('/api/session')
}

export function logout() {
  return request<void>('/api/logout', { method: 'POST' })
}

export function createOrganization(subdomain: string) {
  return request<{ organization: Organization }>('/api/organizations', {
    method: 'POST',
    body: JSON.stringify({ subdomain }),
  })
}

export interface MachinesResponse {
  notConfigured?: boolean
  machines: Machine[]
  users: HeadscaleUser[]
  total: number
}

export function listMachines(params: { q?: string; user?: string; status?: string }) {
  const qs = new URLSearchParams()
  if (params.q) qs.set('q', params.q)
  if (params.user) qs.set('user', params.user)
  if (params.status) qs.set('status', params.status)
  const suffix = qs.toString() ? `?${qs.toString()}` : ''
  return request<MachinesResponse>(`/api/machines${suffix}`)
}

export function registerMachine(user: string, key: string) {
  return request<{ machine: Machine }>('/api/machines/register', {
    method: 'POST',
    body: JSON.stringify({ user, key }),
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

export function setMachineRoutes(id: string, routes: string[]) {
  return request<{ ok: boolean }>(`/api/machines/${id}/routes`, {
    method: 'POST',
    body: JSON.stringify({ routes }),
  })
}

export function expireMachine(id: string) {
  return request<{ ok: boolean }>(`/api/machines/${id}/expire`, { method: 'POST' })
}

export function deleteMachine(id: string) {
  return request<{ ok: boolean }>(`/api/machines/${id}`, { method: 'DELETE' })
}

// Roles mirror headplane's set for UI parity; only owner/admin are
// actually enforced server-side (see handlers/roles.go).
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

export function listMembers() {
  return request<MembersResponse>('/api/members')
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

export function listInvites() {
  return request<{ invites: Invite[] }>('/api/invites')
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
  role: Role
}

export function getInvitePreview(token: string) {
  return request<InvitePreview>(`/api/invites/${token}`)
}

export function acceptInvite(token: string) {
  return request<{ organization: Organization }>(`/api/invites/${token}/accept`, { method: 'POST' })
}

export interface AccessControlResponse {
  notConfigured?: boolean
  content: string
  canManage: boolean
}

export function getAccessControlPolicy() {
  return request<AccessControlResponse>('/api/access-control')
}

export function updateAccessControlPolicy(content: string) {
  return request<{ ok: boolean }>('/api/access-control', {
    method: 'PUT',
    body: JSON.stringify({ content }),
  })
}

export function formatAccessControlPolicy(content: string) {
  return request<{ content: string }>('/api/access-control/format', {
    method: 'POST',
    body: JSON.stringify({ content }),
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
}

export interface AuthKeysResponse {
  notConfigured?: boolean
  authKeys: PreAuthKey[]
  users: HeadscaleUser[]
}

export function listAuthKeys() {
  return request<AuthKeysResponse>('/api/auth-keys')
}

export interface CreateAuthKeyParams {
  userId?: string
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
