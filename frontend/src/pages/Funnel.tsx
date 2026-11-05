import { Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  listFunnelRoutes,
  createFunnelRoute,
  deleteFunnelRoute,
  errorMessage,
  listMachines,
  type FunnelRoute,
} from '../api'
import { usePageTitle } from '../hooks/usePageTitle'
import { useConfirm } from '../lib/confirm'
import { EmptyRow, ErrorAlert, PageHeader, PageSpinner } from '../components/ui'
import { CopyIcon } from '../components/CopyButton'
import { rawTextProps } from '../lib/inputProps'

export default function Funnel() {
  usePageTitle('Funnels')
  const confirm = useConfirm()
  const queryClient = useQueryClient()
  const routesQuery = useQuery({
    queryKey: ['funnel'],
    queryFn: ({ signal }) => listFunnelRoutes(signal),
  })
  const machinesQuery = useQuery({
    queryKey: ['machines', {}],
    queryFn: ({ signal }) => listMachines({}, signal),
  })
  const routes = routesQuery.data?.funnelRoutes ?? []
  const canManage = routesQuery.data?.canManage ?? false
  const machines = machinesQuery.data?.machines ?? []
  const loading = routesQuery.isPending || machinesQuery.isPending
  const hostnameSuffix = routesQuery.data?.hostnameSuffix ?? ''
  const reload = () => queryClient.invalidateQueries({ queryKey: ['funnel'] })

  const [label, setLabel] = useState('')
  const [targetNodeId, setTargetNodeId] = useState('')
  const [targetPort, setTargetPort] = useState('')

  const selectedMachine = machines.find((m) => m.id === targetNodeId)
  const taken = routes.some((r) => r.label === label)
  const preview = taken
    ? `“${label}” is already in use.`
    : label && selectedMachine && targetPort
      ? `https://${label}${hostnameSuffix} → ${selectedMachine.givenName || selectedMachine.name}:${targetPort}`
      : 'Pick a subdomain, a machine and a port.'

  const create = useMutation({
    mutationFn: createFunnelRoute,
    onSuccess: () => {
      setLabel('')
      setTargetNodeId('')
      setTargetPort('')
      return reload()
    },
  })
  const remove = useMutation({
    mutationFn: (r: FunnelRoute) => deleteFunnelRoute(r.id),
    onSuccess: reload,
  })
  const creating = create.isPending
  const createError = create.error ? errorMessage(create.error) : null
  const deletingId = remove.isPending ? remove.variables.id : null
  const failure = remove.error ?? routesQuery.error ?? machinesQuery.error
  const error = failure ? errorMessage(failure) : null

  const handleCreate = (e: React.FormEvent) => {
    e.preventDefault()
    const port = Number(targetPort)
    if (!label.trim() || !targetNodeId || !port || taken) return
    create.mutate({ label: label.trim(), targetNodeId, targetPort: port })
  }

  const handleDelete = async (r: FunnelRoute) => {
    const ok = await confirm({
      title: `Remove https://${r.hostname}?`,
      consequences: ['It will stop being reachable from the internet.'],
      confirmLabel: 'Remove route',
      tone: 'danger',
    })
    if (ok) remove.mutate(r)
  }

  if (loading) {
    return <PageSpinner />
  }

  return (
    <div>
      <PageHeader
        title="Funnels"
        subtitle={`${routes.length} public ${routes.length === 1 ? 'route' : 'routes'}`}
      />

      <div role="alert" className="alert alert-warning mb-4">
        <div>
          <p>
            Funnel exposes a machine&apos;s port to the <strong>entire public internet</strong>, not
            just this tailnet. Anyone with the URL below can reach it, without needing to join this
            tailnet at all. Only create a funnel route for a service you intend to be publicly
            reachable.
          </p>
          <p>A machine running on the ownscale server is needed for funnel functionality.</p>
        </div>
      </div>

      {!canManage && (
        <div className="alert alert-info mb-4">
          <span>Only owners and admins can create or delete funnel routes.</span>
        </div>
      )}
      <ErrorAlert error={error} />

      <div className="card bg-base-100 shadow mb-6">
        <div className="card-body">
          <h2 className="card-title">Routes</h2>
          <div className="overflow-x-auto">
            <table className="table table-sm">
              <thead>
                <tr>
                  <th>Public URL</th>
                  <th>Target machine:port</th>
                  <th>Created by</th>
                  {canManage && <th></th>}
                </tr>
              </thead>
              <tbody>
                {routes.length === 0 && (
                  <EmptyRow colSpan={canManage ? 4 : 3}>No funnel routes configured</EmptyRow>
                )}
                {routes.map((r) => (
                  <tr key={r.id}>
                    <td className="font-mono">
                      <span className="inline-flex items-center gap-1">
                        <a href={`https://${r.hostname}`} target="_blank" rel="noreferrer">
                          https://{r.hostname}
                        </a>
                        <CopyIcon text={`https://${r.hostname}`} label="public URL" />
                      </span>
                    </td>
                    <td className="font-mono">
                      {r.targetNodeMissing ? (
                        <span className="badge badge-warning badge-sm">machine not found</span>
                      ) : (
                        r.targetNodeName
                      )}
                      :{r.targetPort}
                    </td>
                    <td>{r.createdBy || '—'}</td>
                    {canManage && (
                      <td>
                        <button
                          type="button"
                          className="btn btn-ghost btn-xs"
                          disabled={deletingId === r.id}
                          onClick={() => void handleDelete(r)}
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
        </div>
      </div>

      {canManage && (
        <div className="card bg-base-100 shadow">
          <div className="card-body">
            <h2 className="card-title">Create funnel route</h2>
            <ErrorAlert error={createError} />
            <form onSubmit={handleCreate}>
              <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">Subdomain</legend>
                  <label className="input input-bordered input-sm w-full flex items-center gap-0 font-mono">
                    <input {...rawTextProps}
                      id="funnel-label"
                      type="text"
                      className="grow min-w-0"
                      placeholder="status"
                      value={label}
                      onChange={(e) =>
                        setLabel(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))
                      }
                    />
                    <span className="text-xs opacity-60 whitespace-nowrap">{hostnameSuffix}</span>
                  </label>
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">Machine</legend>
                  <select
                    id="funnel-machine"
                    className="select select-bordered select-sm w-full"
                    value={targetNodeId}
                    onChange={(e) => setTargetNodeId(e.target.value)}
                  >
                    <option value="">Select a machine</option>
                    {machines.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.givenName || m.name}
                      </option>
                    ))}
                  </select>
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">Port</legend>
                  <input
                    id="funnel-port"
                    type="text"
                    inputMode="numeric"
                    className="input input-bordered input-sm w-full font-mono"
                    placeholder="8080"
                    value={targetPort}
                    onChange={(e) => setTargetPort(e.target.value.replace(/\D/g, '').slice(0, 5))}
                  />
                </fieldset>
              </div>
              <div className="flex flex-wrap items-center gap-3 mt-3">
                <div className="grow min-w-56 text-sm opacity-70 break-all">{preview}</div>
                <button
                  type="submit"
                  className="btn btn-primary btn-sm"
                  disabled={creating || !label.trim() || !targetNodeId || !targetPort}
                >
                  <Plus className="size-3.5" aria-hidden="true" />
                  Create
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  )
}
