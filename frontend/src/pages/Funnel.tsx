import { useEffect, useState } from 'react'
import {
  listFunnelRoutes,
  createFunnelRoute,
  deleteFunnelRoute,
  listMachines,
  ApiError,
  isAbortError,
  type FunnelRoute,
  type Machine,
} from '../api'
import { usePageTitle } from '../hooks/usePageTitle'

export default function Funnel() {
  usePageTitle('Funnel')
  const [routes, setRoutes] = useState<FunnelRoute[]>([])
  const [machines, setMachines] = useState<Machine[]>([])
  const [canManage, setCanManage] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [label, setLabel] = useState('')
  const [targetNodeId, setTargetNodeId] = useState('')
  const [targetPort, setTargetPort] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)

  const load = async (signal?: AbortSignal) => {
    try {
      const [funnelRes, machinesRes] = await Promise.all([
        listFunnelRoutes(signal),
        listMachines({}, signal),
      ])
      setRoutes(funnelRes.funnelRoutes)
      setCanManage(funnelRes.canManage)
      setMachines(machinesRes.machines)
    } catch (err) {
      if (isAbortError(err)) return
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [])

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault()
    setCreateError(null)
    const port = Number(targetPort)
    if (!label.trim() || !targetNodeId || !port) return
    setCreating(true)
    try {
      await createFunnelRoute({ label: label.trim(), targetNodeId, targetPort: port })
      setLabel('')
      setTargetNodeId('')
      setTargetPort('')
      await load()
    } catch (err) {
      setCreateError(
        err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err),
      )
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async (id: string) => {
    try {
      await deleteFunnelRoute(id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <span className="loading loading-spinner loading-lg"></span>
      </div>
    )
  }

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">Funnel</h1>

      <div role="alert" className="alert alert-warning mb-4">
        <span>
          Funnel exposes a machine&apos;s port to the <strong>entire public internet</strong>, not
          just this tailnet. Anyone with the URL below can reach it, without needing to join this
          tailnet at all. Only create a funnel route for a service you intend to be publicly
          reachable.
        </span>
      </div>

      {!canManage && (
        <div className="alert alert-info mb-4">
          <span>Only owners and admins can create or delete funnel routes.</span>
        </div>
      )}
      {error && (
        <div role="alert" className="alert alert-error mb-4">
          <span>{error}</span>
        </div>
      )}

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
                  <tr>
                    <td colSpan={4} className="text-sm text-base-content/60">
                      No funnel routes configured
                    </td>
                  </tr>
                )}
                {routes.map((r) => (
                  <tr key={r.id}>
                    <td className="font-mono">
                      <a href={`https://${r.hostname}`} target="_blank" rel="noreferrer">
                        https://{r.hostname}
                      </a>
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
                          onClick={() => void handleDelete(r.id)}
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
        </div>
      </div>

      {canManage && (
        <div className="card bg-base-100 shadow">
          <div className="card-body">
            <h2 className="card-title">Create funnel route</h2>
            {createError && (
              <div role="alert" className="alert alert-error mb-2">
                <span>{createError}</span>
              </div>
            )}
            <form className="flex flex-wrap gap-2 items-end" onSubmit={(e) => void handleCreate(e)}>
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
                Create
              </button>
            </form>
          </div>
        </div>
      )}
    </div>
  )
}
