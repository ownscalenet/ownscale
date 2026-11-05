import { Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  getDNSSettings,
  updateDNSSettings,
  errorMessage,
  type DNSConfig,
  type DNSRecord,
  type DNSSettingsResponse,
} from '../api'
import { usePageTitle } from '../hooks/usePageTitle'
import { EmptyRow, ErrorAlert, PageHeader, PageSpinner } from '../components/ui'
import { CopyIcon } from '../components/CopyButton'
import { rawTextProps } from '../lib/inputProps'

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
        {items.length === 0 && (
          <span className="text-sm text-base-content/60">None configured</span>
        )}
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
          <input {...rawTextProps}
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
            <Plus className="size-3.5" aria-hidden="true" />
            Add
          </button>
        </div>
      )}
    </div>
  )
}

export default function DNS() {
  usePageTitle('DNS')
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['dns'], queryFn: ({ signal }) => getDNSSettings(signal) })
  const dns = query.data?.dns
  const canManage = query.data?.canManage ?? false
  const certsAvailable = query.data?.httpsCertificates ?? false

  const [newRecordName, setNewRecordName] = useState('')
  const [newRecordType, setNewRecordType] = useState(RECORD_TYPES[0])
  const [newRecordValue, setNewRecordValue] = useState('')
  const [recordError, setRecordError] = useState<string | null>(null)

  // Every edit saves straight away: it shows at once and is rolled back if
  // the save fails.
  const mutation = useMutation({
    mutationFn: updateDNSSettings,
    onMutate: (next) => {
      const previous = queryClient.getQueryData<DNSSettingsResponse>(['dns'])
      queryClient.setQueryData<DNSSettingsResponse>(['dns'], (old) => old && { ...old, dns: next })
      return { previous }
    },
    onError: (_err, _next, context) => queryClient.setQueryData(['dns'], context?.previous),
  })
  const save = (next: DNSConfig) => mutation.mutate(next)
  const saving = mutation.isPending
  const success = mutation.isSuccess
  const failure = mutation.error ?? query.error
  const error = recordError ?? (failure ? errorMessage(failure) : null)

  if (!dns) {
    return query.error ? (
      <ErrorAlert error={errorMessage(query.error)} inline />
    ) : (
      <PageSpinner />
    )
  }

  const addRecord = () => {
    const name = newRecordName.trim()
    const value = newRecordValue.trim()
    if (!name || !value) return
    // Clients match record names exactly, so a wildcard never resolves.
    if (name.includes('*')) {
      setRecordError('Wildcard DNS records are not supported; add one record per hostname')
      return
    }
    setRecordError(null)
    const record: DNSRecord = { name, type: newRecordType, value }
    save({ ...dns, extraRecords: [...dns.extraRecords, record] })
    setNewRecordName('')
    setNewRecordValue('')
  }

  const removeRecord = (target: DNSRecord) => {
    save({
      ...dns,
      extraRecords: dns.extraRecords.filter(
        (r) => !(r.name === target.name && r.type === target.type && r.value === target.value),
      ),
    })
  }

  return (
    <div>
      <PageHeader title="DNS" />

      {!canManage && (
        <div className="alert alert-warning mb-4">
          <span>Only owners and admins can edit DNS settings for this tailnet.</span>
        </div>
      )}
      <ErrorAlert error={error} />
      {success && (
        <div role="alert" className="alert alert-success mb-4">
          <span>DNS settings saved.</span>
        </div>
      )}

      <div className="flex flex-col gap-6">
        <TailnetName dns={dns} certsAvailable={certsAvailable} />

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
              onChange={(global) => save({ ...dns, nameservers: { ...dns.nameservers, global } })}
            />
          </div>
        </div>

        <SplitDNS dns={dns} canManage={canManage} saving={saving} onSave={save} />

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
                    <EmptyRow colSpan={4}>No records configured</EmptyRow>
                  )}
                  {dns.extraRecords.map((r) => (
                    <tr key={`${r.name}-${r.type}-${r.value}`}>
                      <td className="font-mono">
                        <span className="inline-flex items-center gap-1">
                          {r.name}
                          <CopyIcon text={r.name} label="record name" />
                        </span>
                      </td>
                      <td>{r.type}</td>
                      <td className="font-mono">
                        <span className="inline-flex items-center gap-1">
                          {r.value}
                          <CopyIcon text={r.value} label="record value" />
                        </span>
                      </td>
                      {canManage && (
                        <td>
                          <button
                            type="button"
                            className="btn btn-ghost btn-xs"
                            disabled={saving}
                            onClick={() => removeRecord(r)}
                          >
                            <Trash2 className="size-3.5" aria-hidden="true" />
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
                <input {...rawTextProps}
                  type="text"
                  className="input input-bordered input-sm flex-1"
                  placeholder="Name (e.g. server.example.com)"
                  value={newRecordName}
                  onChange={(e) => {
                    setNewRecordName(e.target.value)
                    setRecordError(null)
                  }}
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
                <input {...rawTextProps}
                  type="text"
                  className="input input-bordered input-sm flex-1"
                  placeholder="Value (e.g. 10.0.0.1)"
                  value={newRecordValue}
                  onChange={(e) => setNewRecordValue(e.target.value)}
                />
                <button type="button" className="btn btn-sm" disabled={saving} onClick={addRecord}>
                  <Plus className="size-3.5" aria-hidden="true" />
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
              onChange={(searchDomains) => save({ ...dns, searchDomains })}
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
                onChange={(e) => save({ ...dns, magicDns: e.target.checked })}
              />
              <span>{dns.magicDns ? 'Enabled' : 'Disabled'}</span>
            </label>
          </div>
        </div>
      </div>
    </div>
  )
}

function SplitDNS({
  dns,
  canManage,
  saving,
  onSave,
}: {
  dns: DNSConfig
  canManage: boolean
  saving: boolean
  onSave: (next: DNSConfig) => void
}) {
  const [domain, setDomain] = useState('')
  const [nameservers, setNameservers] = useState('')
  const entries = Object.entries(dns.nameservers.split).sort(([a], [b]) => a.localeCompare(b))

  const setSplit = (split: Record<string, string[]>) =>
    onSave({ ...dns, nameservers: { ...dns.nameservers, split } })

  const add = () => {
    const d = domain.trim()
    const ns = nameservers
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
    if (!d || ns.length === 0) return
    // Adding an existing domain replaces its nameservers.
    setSplit({ ...dns.nameservers.split, [d]: ns })
    setDomain('')
    setNameservers('')
  }

  const remove = (target: string) => {
    const { [target]: _removed, ...rest } = dns.nameservers.split
    setSplit(rest)
  }

  return (
    <div className="card bg-base-100 shadow">
      <div className="card-body">
        <h2 className="card-title">Split DNS</h2>
        <p className="text-sm text-base-content/70">
          Queries for these domains and their subdomains go to the listed nameservers instead of the
          global ones.
        </p>
        <div className="overflow-x-auto">
          <table className="table table-sm">
            <thead>
              <tr>
                <th>Domain</th>
                <th>Nameservers</th>
                {canManage && <th></th>}
              </tr>
            </thead>
            <tbody>
              {entries.length === 0 && (
                <EmptyRow colSpan={3}>No split DNS domains configured</EmptyRow>
              )}
              {entries.map(([d, ns]) => (
                <tr key={d}>
                  <td className="font-mono">{d}</td>
                  <td className="font-mono">{ns.join(', ')}</td>
                  {canManage && (
                    <td>
                      <button
                        type="button"
                        className="btn btn-ghost btn-xs"
                        disabled={saving}
                        onClick={() => remove(d)}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
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
            <input {...rawTextProps}
              type="text"
              className="input input-bordered input-sm flex-1"
              placeholder="Domain (e.g. corp.example.com)"
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
            />
            <input {...rawTextProps}
              type="text"
              className="input input-bordered input-sm flex-1"
              placeholder="Nameservers (e.g. 10.0.0.53, 10.0.0.54)"
              value={nameservers}
              onChange={(e) => setNameservers(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  add()
                }
              }}
            />
            <button type="button" className="btn btn-sm" disabled={saving} onClick={add}>
              <Plus className="size-3.5" aria-hidden="true" />
              Add
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

function TailnetName({ dns, certsAvailable }: { dns: DNSConfig; certsAvailable: boolean }) {
  return (
    <div className="card bg-base-100 shadow">
      <div className="card-body">
        <h2 className="card-title">Tailnet Name</h2>
        <p className="text-sm text-base-content/70">
          The base domain used for Magic DNS and device names on this tailnet. It is assigned by
          ownscale and cannot be changed.
        </p>
        <div className="flex items-center gap-2">
          <span className="font-mono">{dns.baseDomain}</span>
          <CopyIcon text={dns.baseDomain} label="tailnet name" />
        </div>
        <h3 className="font-semibold mt-4">HTTPS Certificates</h3>
        {!certsAvailable ? (
          <p className="text-sm text-base-content/70">
            Disabled: the server has no DNS provider configured.
          </p>
        ) : !dns.magicDns ? (
          <p className="text-sm text-base-content/70">Disabled: enable Magic DNS to use them.</p>
        ) : (
          <p className="text-sm text-base-content/70">
            Enabled: devices can get a Let&apos;s Encrypt certificate with{' '}
            <code>tailscale cert</code> or <code>tailscale serve</code>. Certificate names are
            published in public{' '}
            <a className="link" href="https://crt.sh/" target="_blank" rel="noreferrer">
              Certificate Transparency logs
            </a>
            .
          </p>
        )}
      </div>
    </div>
  )
}
