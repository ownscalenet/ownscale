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

export default function Funnel() {
  usePageTitle('Funnel')
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
  const reload = () => queryClient.invalidateQueries({ queryKey: ['funnel'] })

  const [label, setLabel] = useState('')
  const [targetNodeId, setTargetNodeId] = useState('')
  const [targetPort, setTargetPort] = useState('')

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
    if (!label.trim() || !targetNodeId || !port) return
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
      <PageHeader title="Funnel" />

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
                  <th>Target machine</th>
                  <th>Target port</th>
                  {canManage && <th></th>}
                </tr>
              </thead>
              <tbody>
                {routes.length === 0 && (
                  <EmptyRow colSpan={4}>No funnel routes configured</EmptyRow>
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
                    <td>
                      {r.targetNodeMissing ? (
                        <span className="badge badge-warning badge-sm">machine not found</span>
                      ) : (
                        r.targetNodeName
                      )}
                    </td>
                    <td className="font-mono">{r.targetPort}</td>
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
            <ErrorAlert error={createError} className="mb-2" />
            <form className="flex flex-wrap gap-2 items-end" onSubmit={handleCreate}>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">Label</legend>
                <input
                  id="funnel-label"
                  type="text"
                  className="input input-bordered input-sm"
                  placeholder="web"
                  value={label}
                  onChange={(e) => setLabel(e.target.value)}
                />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">Machine</legend>
                <select
                  id="funnel-machine"
                  className="select select-bordered select-sm"
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
                  type="number"
                  min={1}
                  max={65535}
                  className="input input-bordered input-sm w-24"
                  placeholder="8080"
                  value={targetPort}
                  onChange={(e) => setTargetPort(e.target.value)}
                />
              </fieldset>
              <button
                type="submit"
                className="btn btn-primary btn-sm"
                disabled={creating || !label.trim() || !targetNodeId || !targetPort}
              >
                <Plus className="size-3.5" aria-hidden="true" />
                Create
              </button>
            </form>
          </div>
        </div>
      )}
    </div>
  )
}
