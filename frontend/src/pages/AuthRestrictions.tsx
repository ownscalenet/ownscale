import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  getAuthRestrictions,
  updateAuthRestrictions,
  errorMessage,
  formatHuJSON,
  type AuthRestrictionsResponse,
} from '../api'
import DiffModal from '../components/DiffModal'
import LineNumberedTextarea from '../components/LineNumberedTextarea'
import { usePageTitle } from '../hooks/usePageTitle'

// Cheatsheet is a static reference for the OIDC login allowlist's HuJSON
// shape, shown next to the editor.
function Cheatsheet() {
  return (
    <div className="card bg-base-100 shadow h-full">
      <div className="card-body">
        <h2 className="card-title">Cheatsheet</h2>
        <p className="text-sm text-base-content/70">
          The allowlist is{' '}
          <a
            className="link"
            href="https://nigeltao.github.io/blog/2021/json-with-commas-comments.html"
            target="_blank"
            rel="noreferrer"
          >
            JWCC
          </a>{' '}
          (JSON with comments and trailing commas allowed). It restricts which OIDC-authenticated
          users can join this organization. Leave a list empty (or omit its key) to not restrict on
          that field.
        </p>

        <div className="divider my-2">Top-level keys</div>
        <dl className="text-sm space-y-2">
          <div>
            <dt className="font-semibold">domains</dt>
            <dd className="text-base-content/70">
              Allowed email domains, e.g. <code>"example.com"</code>
            </dd>
          </div>
          <div>
            <dt className="font-semibold">groups</dt>
            <dd className="text-base-content/70">
              Allowed IdP-provided groups, e.g. <code>"group:eng"</code>
            </dd>
          </div>
          <div>
            <dt className="font-semibold">users</dt>
            <dd className="text-base-content/70">
              Allowed specific email addresses, e.g. <code>"alice@example.com"</code>
            </dd>
          </div>
        </dl>

        <div className="divider my-2">Example</div>
        <pre className="text-xs bg-base-200 rounded p-3 overflow-x-auto whitespace-pre-wrap">
          {`{
  "domains": ["example.com"],
  "groups": ["group:eng"],
  "users": ["alice@example.com"],
}`}
        </pre>

        <div className="divider my-2">Semantics</div>
        <ul className="text-sm text-base-content/70 list-disc list-inside space-y-1">
          <li>
            A user may join if they match <em>any</em> domain, group, or user entry.
          </li>
          <li>All three lists empty means every member of this organization can sign in.</li>
        </ul>
      </div>
    </div>
  )
}

export default function AuthRestrictions() {
  usePageTitle('Auth Restrictions')
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['authRestrictions'],
    queryFn: ({ signal }) => getAuthRestrictions(signal),
  })
  const savedContent = query.data?.content ?? ''
  const canManage = query.data?.canManage ?? false
  // draft holds unsaved edits, so a refetch never overwrites them.
  const [draft, setDraft] = useState<string | null>(null)
  const content = draft ?? savedContent
  const diffRef = useRef<HTMLDialogElement>(null)

  const save = useMutation({
    mutationFn: updateAuthRestrictions,
    onSuccess: (_, saved) => {
      queryClient.setQueryData<AuthRestrictionsResponse>(
        ['authRestrictions'],
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
  const failure = save.error ?? format.error ?? query.error
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
    return (
      <div className="flex items-center justify-center h-64">
        <span className="loading loading-spinner loading-lg"></span>
      </div>
    )
  }

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">Authentication Restrictions</h1>

      {error && (
        <div role="alert" className="alert alert-error mb-4">
          <span>{error}</span>
        </div>
      )}
      {success && (
        <div role="alert" className="alert alert-success mb-4">
          <span>Restrictions saved. Headscale is restarting to apply them.</span>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div className="lg:col-span-2">
          <div className="card bg-base-100 shadow h-full">
            <div className="card-body">
              <div className="flex items-center justify-between mb-2">
                <h2 className="card-title">OIDC allowlist</h2>
                <div className="flex items-center gap-2">
                  {canManage && (
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      disabled={!dirty}
                      onClick={() => diffRef.current?.showModal()}
                    >
                      View diff
                    </button>
                  )}
                  {canManage && (
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      disabled={formatting}
                      onClick={handleFormat}
                    >
                      {formatting ? (
                        <span className="loading loading-spinner loading-xs"></span>
                      ) : null}
                      Format
                    </button>
                  )}
                  {canManage && (
                    <button
                      type="button"
                      className="btn btn-primary btn-sm"
                      disabled={saving || !dirty}
                      onClick={handleSave}
                    >
                      {saving ? <span className="loading loading-spinner loading-xs"></span> : null}
                      Save
                    </button>
                  )}
                </div>
              </div>
              <LineNumberedTextarea value={content} readOnly={!canManage} onChange={setDraft} />
              {!canManage && (
                <p className="text-sm text-base-content/60 mt-2">
                  Only owners and admins can edit authentication restrictions.
                </p>
              )}
              <DiffModal dialogRef={diffRef} savedContent={savedContent} content={content} />
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
