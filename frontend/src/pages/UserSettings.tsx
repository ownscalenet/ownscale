import { useEffect, useState } from 'react'
import {
  listPasskeys,
  deletePasskey,
  passkeyAddBegin,
  passkeyAddFinish,
  ApiError,
  isAbortError,
  type Passkey,
} from '../api'
import { createPasskey, browserSupportsWebAuthn } from '../lib/webauthn'
import { usePageTitle } from '../hooks/usePageTitle'
import { useConfirm } from '../lib/confirm'

export default function UserSettings() {
  usePageTitle('User settings')

  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">User settings</h1>
      </div>

      <PasskeysCard />
    </>
  )
}

function PasskeysCard() {
  const confirm = useConfirm()
  const supported = browserSupportsWebAuthn()
  const [passkeys, setPasskeys] = useState<Passkey[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = async (signal?: AbortSignal) => {
    try {
      const res = await listPasskeys(signal)
      setPasskeys(res.passkeys ?? [])
      setError(null)
    } catch (err) {
      if (isAbortError(err)) return
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [])

  const handleAdd = async () => {
    setError(null)
    setBusy(true)
    try {
      const name = window.prompt('Name this passkey (e.g. "MacBook Touch ID")', 'Passkey') ?? ''
      const options = await passkeyAddBegin(name.trim())
      const response = await createPasskey(options)
      await passkeyAddFinish(response)
      await load()
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err),
      )
    } finally {
      setBusy(false)
    }
  }

  const handleDelete = async (p: Passkey) => {
    const ok = await confirm({
      title: `Delete the passkey "${p.name}"?`,
      message: "This can't be undone.",
      confirmLabel: 'Delete passkey',
      danger: true,
    })
    if (!ok) return
    setBusy(true)
    try {
      await deletePasskey(p.id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card bg-base-100 shadow">
      <div className="card-body">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="card-title">Passkeys</h2>
            <p className="text-sm opacity-60 -mt-1">
              Passkeys let you sign in to the console without a password, using your device&rsquo;s
              biometrics or security key.
            </p>
          </div>
          {supported && (
            <button
              type="button"
              className="btn btn-sm btn-primary"
              disabled={busy}
              onClick={() => void handleAdd()}
            >
              Add a passkey
            </button>
          )}
        </div>

        {error && (
          <div className="alert alert-error mt-2">
            <span>{error}</span>
          </div>
        )}

        {!supported && (
          <div className="alert alert-warning mt-2">
            <span>This browser doesn&rsquo;t support passkeys.</span>
          </div>
        )}

        {passkeys.length > 0 ? (
          <div className="overflow-x-auto mt-2">
            <table className="table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Created</th>
                  <th>Last used</th>
                  <th className="text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {passkeys.map((p) => (
                  <tr key={p.id}>
                    <td>{p.name || 'Passkey'}</td>
                    <td className="whitespace-nowrap">{new Date(p.createdAt).toLocaleString()}</td>
                    <td className="whitespace-nowrap">
                      {p.lastUsedAt ? new Date(p.lastUsedAt).toLocaleString() : '—'}
                    </td>
                    <td className="text-right whitespace-nowrap">
                      <button
                        type="button"
                        className="btn btn-sm btn-ghost text-error"
                        disabled={busy}
                        onClick={() => void handleDelete(p)}
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="p-8 text-center opacity-60">No passkeys yet.</div>
        )}
      </div>
    </div>
  )
}
