// api client: thin wrapper around fetch() for the admin panel's JSON API.
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
// actually enforced server-side (see internal/handlers/roles.go).
export const ROLES = ['owner', 'admin', 'network_admin', 'it_admin', 'auditor', 'member'] as const
export type Role = (typeof ROLES)[number]

export interface Member {
  id: string
  name: string
  email: string
  role: Role
  headscaleUserId: string | null
  headscaleUserName?: string
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

export function linkMember(id: string, headscaleUserId: string) {
  return request<{ ok: boolean }>(`/api/members/${id}/link`, {
    method: 'POST',
    body: JSON.stringify({ headscaleUserId }),
  })
}

export function unlinkMember(id: string) {
  return request<{ ok: boolean }>(`/api/members/${id}/unlink`, { method: 'POST' })
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

export interface HeadscaleUsersResponse {
  notConfigured?: boolean
  users: HeadscaleUser[]
}

export function listHeadscaleUsers() {
  return request<HeadscaleUsersResponse>('/api/headscale-users')
}

export function createHeadscaleUser(name: string) {
  return request<{ user: HeadscaleUser }>('/api/headscale-users', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export function renameHeadscaleUser(id: string, name: string) {
  return request<{ ok: boolean }>(`/api/headscale-users/${id}/rename`, {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export function deleteHeadscaleUser(id: string) {
  return request<{ ok: boolean }>(`/api/headscale-users/${id}`, { method: 'DELETE' })
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
