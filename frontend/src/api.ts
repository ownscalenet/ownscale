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
}

export interface Organization {
  ID: string
  Subdomain: string
  CreatedAt: string
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
  forcedTags: string[] | null
  validTags: string[] | null
  invalidTags: string[] | null
  approvedRoutes: string[] | null
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
  return request<{ user: User }>('/api/session')
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

export function moveMachine(id: string, userId: string) {
  return request<{ ok: boolean }>(`/api/machines/${id}/user`, {
    method: 'POST',
    body: JSON.stringify({ userId }),
  })
}

export function expireMachine(id: string) {
  return request<{ ok: boolean }>(`/api/machines/${id}/expire`, { method: 'POST' })
}

export function deleteMachine(id: string) {
  return request<{ ok: boolean }>(`/api/machines/${id}`, { method: 'DELETE' })
}
