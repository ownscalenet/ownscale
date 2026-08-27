import { useEffect, useRef, useState } from 'react'
import { diffLines } from 'diff'
import { getAccessControlPolicy, updateAccessControlPolicy, formatAccessControlPolicy, ApiError } from '../api'

// cheatsheet is a static reference for headscale's HuJSON ACL policy
// syntax (https://headscale.net/stable/ref/acls/). It never calls the API
// -- it's just documentation rendered alongside the editor, mirroring
// headplane's Access Control tab.
function Cheatsheet() {
  return (
    <div className="card bg-base-100 shadow h-full">
      <div className="card-body">
        <h2 className="card-title">Cheatsheet</h2>
        <p className="text-sm text-base-content/70">
          The policy is <a className="link" href="https://headscale.net/stable/ref/acls/" target="_blank" rel="noreferrer">HuJSON</a>{' '}
          (JSON with comments and trailing commas allowed).
        </p>

        <div className="divider my-2">Top-level keys</div>
        <dl className="text-sm space-y-2">
          <div>
            <dt className="font-semibold">groups</dt>
            <dd className="text-base-content/70">Named lists of users, e.g. <code>"group:eng": ["alice@", "bob@"]</code></dd>
          </div>
          <div>
            <dt className="font-semibold">tagOwners</dt>
            <dd className="text-base-content/70">Who can assign a tag, e.g. <code>"tag:server": ["group:eng"]</code></dd>
          </div>
          <div>
            <dt className="font-semibold">hosts</dt>
            <dd className="text-base-content/70">Named IP/CIDR aliases, e.g. <code>"internal": "10.0.0.0/8"</code></dd>
          </div>
          <div>
            <dt className="font-semibold">acls</dt>
            <dd className="text-base-content/70">
              List of rules: <code>action</code> (<code>"accept"</code>), <code>src</code>, <code>dst</code> (host:port).
            </dd>
          </div>
          <div>
            <dt className="font-semibold">ssh</dt>
            <dd className="text-base-content/70">
              SSH access rules: <code>action</code>, <code>src</code>, <code>dst</code>, <code>users</code>.
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
}`}
        </pre>

        <div className="divider my-2">Selectors</div>
        <ul className="text-sm text-base-content/70 list-disc list-inside space-y-1">
          <li><code>*</code> - everyone / everything</li>
          <li><code>tag:name</code> - nodes with a tag</li>
          <li><code>group:name</code> - a defined group</li>
          <li><code>user@</code> - a specific user</li>
          <li><code>host:port</code> or <code>host:*</code> - a destination</li>
        </ul>
      </div>
    </div>
  )
}

// DiffModal renders a line-level diff between the saved and current policy
// content inside a daisyUI dialog (same pattern as Users.tsx's modals).
function DiffModal({ dialogRef, savedContent, content }: { dialogRef: React.RefObject<HTMLDialogElement | null>; savedContent: string; content: string }) {
  const parts = diffLines(savedContent, content)
  return (
    <dialog ref={dialogRef} className="modal">
      <div className="modal-box max-w-3xl">
        <h3 className="font-bold text-lg mb-2">Diff since last save</h3>
        <pre className="text-xs bg-base-200 rounded p-3 overflow-x-auto whitespace-pre-wrap max-h-[28rem] overflow-y-auto">
          {parts.length === 0 || parts.every((p) => !p.added && !p.removed) ? (
            <span className="text-base-content/60">No changes.</span>
          ) : (
            parts.map((part, i) => {
              const lines = part.value.endsWith('\n') ? part.value.slice(0, -1).split('\n') : part.value.split('\n')
              return (
                <span key={i} className={part.added ? 'bg-success/20 text-success-content block' : part.removed ? 'bg-error/20 text-error-content block' : 'block'}>
                  {lines.map((line, li) => (
                    <span key={li} className="block">
                      {part.added ? '+ ' : part.removed ? '- ' : '  '}
                      {line}
                    </span>
                  ))}
                </span>
              )
            })
          )}
        </pre>
        <div className="modal-action">
          <form method="dialog">
            <button className="btn">Close</button>
          </form>
        </div>
      </div>
      <form method="dialog" className="modal-backdrop">
        <button>close</button>
      </form>
    </dialog>
  )
}

export default function AccessControl() {
  const [content, setContent] = useState('')
  const [savedContent, setSavedContent] = useState('')
  const [canManage, setCanManage] = useState(false)
  const [notConfigured, setNotConfigured] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [formatting, setFormatting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState(false)
  const diffRef = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    void (async () => {
      try {
        const res = await getAccessControlPolicy()
        setContent(res.content)
        setSavedContent(res.content)
        setCanManage(res.canManage)
        setNotConfigured(res.notConfigured ?? false)
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      } finally {
        setLoading(false)
      }
    })()
  }, [])

  const handleSave = async () => {
    setSaving(true)
    setError(null)
    setSuccess(false)
    try {
      await updateAccessControlPolicy(content)
      setSavedContent(content)
      setSuccess(true)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  const handleFormat = async () => {
    setFormatting(true)
    setError(null)
    setSuccess(false)
    try {
      const res = await formatAccessControlPolicy(content)
      setContent(res.content)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err))
    } finally {
      setFormatting(false)
    }
  }

  const dirty = content !== savedContent

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <span className="loading loading-spinner loading-lg"></span>
      </div>
    )
  }

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">Access Control</h1>

      {notConfigured && (
        <div className="alert alert-warning mb-4">
          <span>Your organization isn't active yet, so its access control policy can't be edited.</span>
        </div>
      )}

      {error && (
        <div role="alert" className="alert alert-error mb-4">
          <span>{error}</span>
        </div>
      )}
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
                  <button
                    type="button"
                    className="btn btn-ghost btn-sm"
                    disabled={!dirty}
                    onClick={() => diffRef.current?.showModal()}
                  >
                    View diff
                  </button>
                  {canManage && (
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      disabled={formatting || notConfigured}
                      onClick={() => void handleFormat()}
                    >
                      {formatting ? <span className="loading loading-spinner loading-xs"></span> : null}
                      Format
                    </button>
                  )}
                  {canManage && (
                    <button
                      type="button"
                      className="btn btn-primary btn-sm"
                      disabled={saving || notConfigured || !dirty}
                      onClick={() => void handleSave()}
                    >
                      {saving ? <span className="loading loading-spinner loading-xs"></span> : null}
                      Save
                    </button>
                  )}
                </div>
              </div>
              <textarea
                className="textarea textarea-bordered w-full font-mono text-sm"
                style={{ minHeight: '32rem' }}
                spellCheck={false}
                value={content}
                readOnly={!canManage}
                onChange={(e) => setContent(e.target.value)}
              />
              {!canManage && (
                <p className="text-sm text-base-content/60 mt-2">Only owners and admins can edit the access control policy.</p>
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
