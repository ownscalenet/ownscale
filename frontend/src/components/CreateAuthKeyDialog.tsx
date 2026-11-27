import { KeyRound } from 'lucide-react'
import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { useAuth } from '../authContext'
import { createAuthKey, errorMessage, type AssignableUser } from '../api'
import Modal from './Modal'
import { CopyIcon } from './CopyButton'
import { ErrorAlert } from './ui'
import { rawTextProps } from '../lib/inputProps'

// CreateAuthKeyDialog creates a pre-auth key. Without canManage, keys can
// only be untagged ones for the caller.
export default function CreateAuthKeyDialog({
  onClose,
  users,
  canManage,
  onCreated,
}: {
  onClose: () => void
  users: AssignableUser[]
  canManage: boolean
  onCreated: () => void
}) {
  const [tagOnly, setTagOnly] = useState(false)
  const [memberId, setMemberId] = useState('')
  const [tagsInput, setTagsInput] = useState('')
  const [expiryDays, setExpiryDays] = useState(90)
  const [reusable, setReusable] = useState(false)
  const [ephemeral, setEphemeral] = useState(false)
  const { organization, refresh } = useAuth()
  const loginServer = `${window.location.protocol}//${organization?.Domain}`
  const [validationError, setValidationError] = useState<string | null>(null)
  const [createdKey, setCreatedKey] = useState<string | null>(null)
  const create = useMutation({
    mutationFn: createAuthKey,
    onSuccess: (res) => {
      setCreatedKey(res.authKey.key)
      onCreated()
      // The first key starts the org's headscale, changing its status.
      void refresh()
    },
  })
  const busy = create.isPending
  const error = validationError ?? (create.error ? errorMessage(create.error) : null)

  const parsedTags = tagsInput
    .split(',')
    .map((t) => t.trim())
    .filter(Boolean)
    .map((t) => (t.startsWith('tag:') ? t : `tag:${t}`))

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setValidationError(null)
    if (!tagOnly && !memberId) {
      setValidationError('Choose a user, or toggle "Tag only" and provide ACL tags.')
      return
    }
    if (tagOnly && parsedTags.length === 0) {
      setValidationError('At least one ACL tag is required for a tag-only key.')
      return
    }
    create.mutate({
      memberId: tagOnly ? undefined : memberId,
      aclTags: parsedTags,
      expiryDays,
      reusable,
      ephemeral,
    })
  }

  const command = `tailscale up --login-server=${loginServer} --authkey ${createdKey}`

  return (
    <Modal
      open
      onClose={onClose}
      title={createdKey ? 'Pre-auth key created' : 'Create pre-auth key'}
    >
      {createdKey ? (
        <>
          <p className="text-sm opacity-60 mb-2">
            Copy this key now &mdash; it won&rsquo;t be shown again.
          </p>
          <div className="relative mb-4">
            <div className="mockup-code text-sm overflow-x-auto">
              <pre data-prefix="$">
                <code>{createdKey}</code>
              </pre>
            </div>
            {/* The code block is neutral in every theme; the ghost button colors itself with base-content. */}
            <span className="absolute top-2 right-2 [--color-base-content:var(--color-neutral-content)]">
              <CopyIcon text={createdKey} label="key" />
            </span>
          </div>
          <p className="text-sm opacity-60 mb-2">Use it to register a new device:</p>
          <div className="relative mb-4">
            <div className="mockup-code text-xs overflow-x-auto">
              <pre data-prefix="$">
                <code>{command}</code>
              </pre>
            </div>
            <span className="absolute top-2 right-2 [--color-base-content:var(--color-neutral-content)]">
              <CopyIcon text={command} label="command" />
            </span>
          </div>
          <div className="modal-action">
            <button type="button" className="btn btn-primary" onClick={onClose}>
              Done
            </button>
          </div>
        </>
      ) : (
        <>
          <ErrorAlert error={error} />
          <form className="space-y-4" onSubmit={handleSubmit}>
            {canManage && (
              <label className="fieldset-label cursor-pointer gap-3">
                <input
                  type="checkbox"
                  className="checkbox checkbox-sm"
                  checked={tagOnly}
                  onChange={(e) => setTagOnly(e.target.checked)}
                />
                <span>Tag only (no owning user)</span>
              </label>
            )}

            {!tagOnly && (
              <fieldset className="fieldset">
                <legend className="fieldset-legend">User</legend>
                <select
                  className="select select-bordered w-full"
                  value={memberId}
                  onChange={(e) => setMemberId(e.target.value)}
                  required={!tagOnly}
                >
                  <option value="">Select a user…</option>
                  {users.map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.name || u.email}
                    </option>
                  ))}
                </select>
              </fieldset>
            )}

            {canManage && (
              <fieldset className="fieldset">
                <legend className="fieldset-legend">ACL tags{tagOnly && ' (required)'}</legend>
                <input
                  {...rawTextProps}
                  type="text"
                  className="input input-bordered w-full font-mono"
                  placeholder="tag:server, tag:prod"
                  value={tagsInput}
                  onChange={(e) => setTagsInput(e.target.value)}
                />
                <p className="text-sm opacity-60 mt-1">
                  Comma-separated. The <code>tag:</code> prefix is added automatically.
                </p>
              </fieldset>
            )}

            <fieldset className="fieldset">
              <legend className="fieldset-legend">Expires in (days)</legend>
              <input
                type="number"
                min={1}
                className="input input-bordered w-full"
                value={expiryDays}
                onChange={(e) => setExpiryDays(Number(e.target.value))}
                required
              />
            </fieldset>

            <label className="fieldset-label cursor-pointer gap-3">
              <input
                type="checkbox"
                className="checkbox checkbox-sm"
                checked={reusable}
                onChange={(e) => setReusable(e.target.checked)}
              />
              <span>Reusable</span>
            </label>
            <label className="fieldset-label cursor-pointer gap-3">
              <input
                type="checkbox"
                className="checkbox checkbox-sm"
                checked={ephemeral}
                onChange={(e) => setEphemeral(e.target.checked)}
              />
              <span>Ephemeral</span>
            </label>

            <div className="modal-action">
              <button type="submit" className="btn btn-primary" disabled={busy}>
                <KeyRound className="size-3.5" aria-hidden="true" />
                Create
              </button>
              <button type="button" className="btn btn-ghost" onClick={onClose}>
                Cancel
              </button>
            </div>
          </form>
        </>
      )}
    </Modal>
  )
}
