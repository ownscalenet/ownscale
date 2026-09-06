import { useEffect, useState } from 'react'
import { getDNSSettings, updateDNSSettings, ApiError, type DNSConfig, type DNSRecord } from '../api'
import { usePageTitle } from '../hooks/usePageTitle'

const RECORD_TYPES = ['A', 'AAAA', 'CNAME']

// ListEditor renders a simple list of chips with per-item remove buttons
// plus an inline add form, reused for nameservers and search domains
// (both are just string lists).
function ListEditor({
  items,
  placeholder,
  disabled,
  onChange,
}: {
  items: string[]
  placeholder: string
  disabled: boolean
  onChange: (items: string[]) => void
}) {
  const [value, setValue] = useState('')

  const add = () => {
    const v = value.trim()
    if (!v || items.includes(v)) return
    onChange([...items, v])
    setValue('')
  }

  return (
    <div>
      <div className="flex flex-wrap gap-2 mb-3">
        {items.length === 0 && <span className="text-sm text-base-content/60">None configured</span>}
        {items.map((item) => (
          <span key={item} className="badge badge-lg gap-2">
            {item}
            {!disabled && (
              <button
                type="button"
                className="cursor-pointer"
                aria-label={`Remove ${item}`}
                onClick={() => onChange(items.filter((i) => i !== item))}
              >
                ✕
              </button>
            )}
          </span>
        ))}
      </div>
      {!disabled && (
        <div className="flex gap-2">
          <input
            type="text"
            className="input input-bordered input-sm flex-1"
            placeholder={placeholder}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                add()
              }
            }}
          />
          <button type="button" className="btn btn-sm" onClick={add}>
            Add
          </button>
        </div>
      )}
    </div>
  )
}

export default function DNS() {
  usePageTitle('DNS')
  const [dns, setDns] = useState<DNSConfig | null>(null)
  const [savedDns, setSavedDns] = useState<DNSConfig | null>(null)
  const [canManage, setCanManage] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState(false)

  const [newRecordName, setNewRecordName] = useState('')
  const [newRecordType, setNewRecordType] = useState(RECORD_TYPES[0])
  const [newRecordValue, setNewRecordValue] = useState('')

  useEffect(() => {
    void (async () => {
      try {
        const res = await getDNSSettings()
        setDns(res.dns)
        setSavedDns(res.dns)
        setCanManage(res.canManage)
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      } finally {
        setLoading(false)
      }
    })()
  }, [])

  const save = async (next: DNSConfig) => {
    setDns(next)
    setSaving(true)
    setError(null)
    setSuccess(false)
    try {
      await updateDNSSettings(next)
      setSavedDns(next)
      setSuccess(true)
    } catch (err) {
      setDns(savedDns)
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  if (loading || !dns) {
    return (
      <div className="flex items-center justify-center h-64">
        <span className="loading loading-spinner loading-lg"></span>
      </div>
    )
  }

  const addRecord = () => {
    const name = newRecordName.trim()
    const value = newRecordValue.trim()
    if (!name || !value) return
    const record: DNSRecord = { name, type: newRecordType, value }
    void save({ ...dns, extraRecords: [...dns.extraRecords, record] })
    setNewRecordName('')
    setNewRecordValue('')
  }

  const removeRecord = (target: DNSRecord) => {
    void save({
      ...dns,
      extraRecords: dns.extraRecords.filter((r) => !(r.name === target.name && r.type === target.type && r.value === target.value)),
    })
  }

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">DNS</h1>

      {!canManage && (
        <div className="alert alert-warning mb-4">
          <span>Only owners and admins can edit DNS settings for this tailnet.</span>
        </div>
      )}
      {error && (
        <div role="alert" className="alert alert-error mb-4">
          <span>{error}</span>
        </div>
      )}
      {success && (
        <div role="alert" className="alert alert-success mb-4">
          <span>DNS settings saved. Headscale is restarting to apply them.</span>
        </div>
      )}

      <div className="flex flex-col gap-6">
        <RenameTailnet dns={dns} disabled={!canManage || saving} onSave={save} />

        <div className="card bg-base-100 shadow">
          <div className="card-body">
            <h2 className="card-title">Nameservers</h2>
            <p className="text-sm text-base-content/70">
              Global nameservers used to resolve DNS queries from devices on this tailnet.
            </p>
            <ListEditor
              items={dns.nameservers.global}
              placeholder="1.1.1.1"
              disabled={!canManage || saving}
              onChange={(global) => void save({ ...dns, nameservers: { global } })}
            />
          </div>
        </div>

        <div className="card bg-base-100 shadow">
          <div className="card-body">
            <h2 className="card-title">DNS Records</h2>
            <p className="text-sm text-base-content/70">
              Static DNS records resolved for every device on this tailnet.
            </p>
            <div className="overflow-x-auto">
              <table className="table table-sm">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Type</th>
                    <th>Value</th>
                    {canManage && <th></th>}
                  </tr>
                </thead>
                <tbody>
                  {dns.extraRecords.length === 0 && (
                    <tr>
                      <td colSpan={4} className="text-sm text-base-content/60">
                        No records configured
                      </td>
                    </tr>
                  )}
                  {dns.extraRecords.map((r) => (
                    <tr key={`${r.name}-${r.type}-${r.value}`}>
                      <td className="font-mono">{r.name}</td>
                      <td>{r.type}</td>
                      <td className="font-mono">{r.value}</td>
                      {canManage && (
                        <td>
                          <button
                            type="button"
                            className="btn btn-ghost btn-xs"
                            disabled={saving}
                            onClick={() => removeRecord(r)}
                          >
                            Remove
                          </button>
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {canManage && (
              <div className="flex flex-wrap gap-2 mt-2">
                <input
                  type="text"
                  className="input input-bordered input-sm flex-1"
                  placeholder="Name (e.g. server.example.com)"
                  value={newRecordName}
                  onChange={(e) => setNewRecordName(e.target.value)}
                />
                <select
                  className="select select-bordered select-sm"
                  value={newRecordType}
                  onChange={(e) => setNewRecordType(e.target.value)}
                >
                  {RECORD_TYPES.map((t) => (
                    <option key={t} value={t}>
                      {t}
                    </option>
                  ))}
                </select>
                <input
                  type="text"
                  className="input input-bordered input-sm flex-1"
                  placeholder="Value (e.g. 10.0.0.1)"
                  value={newRecordValue}
                  onChange={(e) => setNewRecordValue(e.target.value)}
                />
                <button type="button" className="btn btn-sm" disabled={saving} onClick={addRecord}>
                  Add
                </button>
              </div>
            )}
          </div>
        </div>

        <div className="card bg-base-100 shadow">
          <div className="card-body">
            <h2 className="card-title">Search Domains</h2>
            <p className="text-sm text-base-content/70">
              Domains appended to unqualified hostname lookups on devices in this tailnet.
            </p>
            <ListEditor
              items={dns.searchDomains}
              placeholder="internal.example.com"
              disabled={!canManage || saving}
              onChange={(searchDomains) => void save({ ...dns, searchDomains })}
            />
          </div>
        </div>

        <div className="card bg-base-100 shadow">
          <div className="card-body">
            <h2 className="card-title">Magic DNS</h2>
            <p className="text-sm text-base-content/70">
              Automatically register domain names for each device on the tailnet. Devices are
              accessible at <code>[device].{dns.baseDomain}</code> when Magic DNS is enabled.
            </p>
            <label className="fieldset-label cursor-pointer gap-3">
              <input
                type="checkbox"
                className="toggle toggle-primary"
                checked={dns.magicDns}
                disabled={!canManage || saving}
                onChange={(e) => void save({ ...dns, magicDns: e.target.checked })}
              />
              <span>{dns.magicDns ? 'Enabled' : 'Disabled'}</span>
            </label>
          </div>
        </div>
      </div>
    </div>
  )
}

function RenameTailnet({
  dns,
  disabled,
  onSave,
}: {
  dns: DNSConfig
  disabled: boolean
  onSave: (next: DNSConfig) => void
}) {
  const [name, setName] = useState(dns.baseDomain)

  useEffect(() => setName(dns.baseDomain), [dns.baseDomain])

  return (
    <div className="card bg-base-100 shadow">
      <div className="card-body">
        <h2 className="card-title">Tailnet Name</h2>
        <p className="text-sm text-base-content/70">
          The base domain used for Magic DNS and device names on this tailnet.
        </p>
        <div className="flex gap-2">
          <input
            type="text"
            className="input input-bordered input-sm flex-1 max-w-md"
            value={name}
            disabled={disabled}
            onChange={(e) => setName(e.target.value)}
          />
          <button
            type="button"
            className="btn btn-primary btn-sm"
            disabled={disabled || name.trim() === '' || name === dns.baseDomain}
            onClick={() => onSave({ ...dns, baseDomain: name.trim() })}
          >
            Save
          </button>
        </div>
      </div>
    </div>
  )
}
