import { AlignLeft, Save } from 'lucide-react'
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  getAccessControlPolicy,
  updateAccessControlPolicy,
  errorMessage,
  formatHuJSON,
  isNotConfigured,
  type AccessControlResponse,
} from '../api'
import DiffModal from '../components/DiffModal'
import LineNumberedTextarea from '../components/LineNumberedTextarea'
import { usePageTitle } from '../hooks/usePageTitle'
import { ErrorAlert, PageHeader, PageSpinner } from '../components/ui'

// Cheatsheet is a static reference for headscale's HuJSON ACL policy syntax
// (https://headscale.net/stable/ref/acls/), shown next to the editor.
function Cheatsheet() {
  return (
    <div className="card bg-base-100 shadow h-full">
      <div className="card-body">
        <h2 className="card-title">Cheatsheet</h2>
        <p className="text-sm text-base-content/70">
          The policy is{' '}
          <a
            className="link"
            href="https://nigeltao.github.io/blog/2021/json-with-commas-comments.html"
            target="_blank"
            rel="noreferrer"
          >
            JWCC
          </a>{' '}
          (JSON with comments and trailing commas allowed). Check the{' '}
          <a
            className="link"
            href="https://headscale.net/stable/ref/policy/"
            target="_blank"
            rel="noreferrer"
          >
            full reference
          </a>{' '}
          for what is achievable using policies
        </p>

        <div className="divider my-2">Top-level keys</div>
        <dl className="text-sm space-y-2">
          <div>
            <dt className="font-semibold">groups</dt>
            <dd className="text-base-content/70">
              Named lists of users, e.g. <code>"group:eng": ["alice@", "bob@"]</code>
            </dd>
          </div>
          <div>
            <dt className="font-semibold">tagOwners</dt>
            <dd className="text-base-content/70">
              Who can assign a tag, e.g. <code>"tag:server": ["group:eng"]</code>
            </dd>
          </div>
          <div>
            <dt className="font-semibold">hosts</dt>
            <dd className="text-base-content/70">
              Named IP/CIDR aliases, e.g. <code>"internal": "10.0.0.0/8"</code>
            </dd>
          </div>
          <div>
            <dt className="font-semibold">acls</dt>
            <dd className="text-base-content/70">
              List of rules: <code>action</code> (<code>"accept"</code>), <code>src</code>,{' '}
              <code>dst</code> (host:port).
            </dd>
          </div>
          <div>
            <dt className="font-semibold">ssh</dt>
            <dd className="text-base-content/70">
              Tailscale SSH rules: <code>action</code> (<code>"accept"</code>), <code>src</code>,{' '}
              <code>dst</code>, <code>users</code> (<code>"root"</code>,{' '}
              <code>"autogroup:nonroot"</code>). <code>src</code> and <code>dst</code> can't be{' '}
              <code>*</code>; use <code>autogroup:self</code> for a user's own machines. Target
              machines must run <code>tailscale up --ssh</code>, and <code>acls</code> must allow
              port 22.
            </dd>
          </div>
        </dl>

        <div className="divider my-2">Example</div>
        <pre className="text-xs bg-base-200 rounded p-3 overflow-x-auto whitespace-pre-wrap">
          {`{
  "groups": {
    "group:eng": ["alice@", "bob@"],
  },
  "acls": [
    {
      "action": "accept",
      "src": ["group:eng"],
      "dst": ["*:*"],
    },
  ],
  "ssh": [
    {
      "action": "accept",
      "src": ["autogroup:member"],
      "dst": ["autogroup:self"],
      "users": ["autogroup:nonroot", "root"],
    },
  ],
}`}
        </pre>

        <div className="divider my-2">Selectors</div>
        <ul className="text-sm text-base-content/70 list-disc list-inside space-y-1">
          <li>
            <code>*</code> - everyone / everything
          </li>
          <li>
            <code>tag:name</code> - nodes with a tag
          </li>
          <li>
            <code>group:name</code> - a defined group
          </li>
          <li>
            <code>user@</code> - a specific user
          </li>
          <li>
            <code>autogroup:member</code> - every user&rsquo;s machines,{' '}
            <code>autogroup:tagged</code> - tagged machines
          </li>
          <li>
            <code>autogroup:self</code> - the user&rsquo;s own machines (as a destination)
          </li>
          <li>
            <code>host:port</code> or <code>host:*</code> - a destination
          </li>
        </ul>
      </div>
    </div>
  )
}

export default function AccessControl() {
  usePageTitle('Access Control')
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['acl'],
    queryFn: ({ signal }) => getAccessControlPolicy(signal),
  })
  const savedContent = query.data?.content ?? ''
  const canManage = query.data?.canManage ?? false
  const notConfigured = isNotConfigured(query.error)
  // draft holds unsaved edits, so a refetch never overwrites them.
  const [draft, setDraft] = useState<string | null>(null)
  const content = draft ?? savedContent
  const [showDiff, setShowDiff] = useState(false)

  const save = useMutation({
    mutationFn: updateAccessControlPolicy,
    onSuccess: (_, saved) => {
      queryClient.setQueryData<AccessControlResponse>(
        ['acl'],
        (old) => old && { ...old, content: saved },
      )
      setDraft(null)
    },
  })
  const format = useMutation({
    mutationFn: formatHuJSON,
    onSuccess: (res) => setDraft(res.content),
  })
  const saving = save.isPending
  const formatting = format.isPending
  const success = save.isSuccess
  const failure = save.error ?? format.error ?? (notConfigured ? null : query.error)
  const error = failure ? errorMessage(failure) : null

  const handleSave = () => {
    format.reset()
    save.mutate(content)
  }

  const handleFormat = () => {
    save.reset()
    format.mutate(content)
  }

  const dirty = content !== savedContent

  if (query.isPending) {
    return <PageSpinner />
  }

  return (
    <div>
      <PageHeader title="Access Control" />

      {notConfigured && (
        <div className="alert alert-warning mb-4">
          <span>
            Your organization isn't active yet, so its access control policy can't be edited.
          </span>
        </div>
      )}

      <ErrorAlert error={error} />
      {success && (
        <div role="alert" className="alert alert-success mb-4">
          <span>Policy saved. Headscale is restarting to apply it.</span>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div className="lg:col-span-2">
          <div className="card bg-base-100 shadow h-full">
            <div className="card-body">
              <div className="flex items-center justify-between mb-2">
                <h2 className="card-title">Policy file (acl.hujson)</h2>
                <div className="flex items-center gap-2">
                  {canManage && (
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      disabled={!dirty}
                      onClick={() => setShowDiff(true)}
                    >
                      View diff
                    </button>
                  )}
                  {canManage && (
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      disabled={formatting || notConfigured}
                      onClick={handleFormat}
                    >
                      {formatting ? (
                        <span className="loading loading-spinner loading-xs"></span>
                      ) : null}
                      <AlignLeft className="size-3.5" aria-hidden="true" />
                      Format
                    </button>
                  )}
                  {canManage && (
                    <button
                      type="button"
                      className="btn btn-primary btn-sm"
                      disabled={saving || notConfigured || !dirty}
                      onClick={handleSave}
                    >
                      {saving ? <span className="loading loading-spinner loading-xs"></span> : null}
                      <Save className="size-3.5" aria-hidden="true" />
                      Save
                    </button>
                  )}
                </div>
              </div>
              <LineNumberedTextarea value={content} readOnly={!canManage} onChange={setDraft} />
              {!canManage && (
                <p className="text-sm text-base-content/60 mt-2">
                  Only owners and admins can edit the access control policy.
                </p>
              )}
              {showDiff && (
                <DiffModal
                  onClose={() => setShowDiff(false)}
                  savedContent={savedContent}
                  content={content}
                />
              )}
            </div>
          </div>
        </div>

        <div className="lg:col-span-1">
          <Cheatsheet />
        </div>
      </div>
    </div>
  )
}
