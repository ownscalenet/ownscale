import { useEffect, useRef, useState } from 'react'
import type { IPN } from '@tailscale/connect'
import { Link, useParams } from 'react-router-dom'
import { TerminalSquare } from 'lucide-react'
import { createSSHSession, errorMessage, type SSHSession } from '../api'
import { useAuth } from '../auth'
import { usePageTitle } from '../hooks/usePageTitle'
import { ErrorAlert, PageHeader } from '../components/ui'

// pendingSession is a tailnet-connected client waiting for the terminal
// container to render.
interface pendingSession {
  ipn: IPN
  run: typeof import('@tailscale/connect').runSSHSession
  target: string
  username: string
}

// phase dictates what the page shows: the connect form, the progress while
// the WebAssembly client joins the tailnet, the live terminal, or the
// "session ended" state.
type phase = 'form' | 'connecting' | 'connected' | 'done'

// sshUserKey is where the username last used for a machine is remembered,
// so reconnecting doesn't mean retyping it.
function sshUserKey(machineId: string): string {
  return `ownscale:ssh-user:${machineId}`
}

// defaultUsername guesses the remote username: the one last used for this
// machine, else the local part of the signed-in user's email.
function defaultUsername(machineId: string, email: string | undefined): string {
  return localStorage.getItem(sshUserKey(machineId)) ?? (email ?? '').split('@')[0]
}

// muteTailscaleLogs stops the upstream WebAssembly build from uploading
// client logs to Tailscale's log service: ownscale is self-hosted, so
// nothing should leave the tailnet. It returns a function restoring the
// original fetch.
function muteTailscaleLogs(): () => void {
  const original = window.fetch
  window.fetch = (input, init) => {
    const url =
      typeof input === 'string' ? input : input instanceof URL ? input.href : (input as Request).url
    if (url.includes('log.tailscale.com')) {
      return Promise.resolve(
        new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
      )
    }
    return original(input, init)
  }
  return () => {
    window.fetch = original
  }
}

export default function SSH() {
  const { id = '' } = useParams()
  const { user } = useAuth()
  const [username, setUsername] = useState(() => defaultUsername(id, user?.Email))
  const [session, setSession] = useState<SSHSession | null>(null)
  const [phase, setPhase] = useState<phase>('form')
  const [progress, setProgress] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState<pendingSession | null>(null)
  const termRef = useRef<HTMLDivElement>(null)
  const cleanupRef = useRef<(() => void)[]>([])
  const startedRef = useRef(false)

  // The terminal can only be attached once its container exists, which is
  // one render after the client reports Running.
  useEffect(() => {
    if (!pending || !termRef.current || startedRef.current) return
    startedRef.current = true
    pending.run(
      termRef.current,
      { username: pending.username, hostname: pending.target },
      pending.ipn,
      {
        onConnectionProgress: setProgress,
        onConnected: () => setProgress(''),
        onDone: () => setPhase('done'),
        onError: setError,
      },
      { fontSize: 13 },
    )
  }, [pending])

  // Leaving the page (client-side navigation included) tears the node
  // down instead of waiting for headscale's ephemeral timeout.
  useEffect(() => {
    const cleanups = cleanupRef.current
    return () => {
      for (const fn of cleanups.splice(0)) fn()
    }
  }, [])

  const machineName = session?.machineName ?? 'machine'
  usePageTitle(phase === 'form' ? 'SSH' : `SSH ${machineName}`)

  // Once the username is known it's worth remembering, even if the
  // connection itself fails.
  useEffect(() => {
    if (phase !== 'form' && username) {
      localStorage.setItem(sshUserKey(id), username)
    }
  }, [phase, username, id])

  const connect = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username.trim()) return
    setError(null)
    setPhase('connecting')
    setProgress('Requesting a tailnet key...')
    try {
      const s = await createSSHSession(id)
      setSession(s)
      await runSession(s, username.trim())
    } catch (err) {
      setError(errorMessage(err))
      setPhase('done')
    }
  }

  const runSession = async (s: SSHSession, user: string) => {
    cleanupRef.current.push(muteTailscaleLogs())
    setProgress('Loading the Tailscale client...')
    await import('@tailscale/connect/pkg.css')
    const { createIPN, runSSHSession } = await import('@tailscale/connect')
    const ipn = await createIPN({
      controlURL: s.controlURL,
      authKey: s.authKey,
      hostname: s.hostname,
      wasmURL: '/assets/main.wasm',
      panicHandler: (err) => setError(`Tailscale client stopped: ${err}`),
    })
    cleanupRef.current.push(() => ipn.logout())
    setProgress('Joining the tailnet...')
    ipn.run({
      notifyState: (state) => {
        if (state === 'NeedsLogin') {
          ipn.login()
          return
        }
        if (state === 'NeedsMachineAuth') {
          setError('This tailnet requires manual approval of new machines.')
          return
        }
        if (state !== 'Running') return
        setPhase('connected')
        setPending({ ipn, run: runSSHSession, target: s.target, username: user })
      },
      notifyNetMap: () => {},
      notifyBrowseToURL: () => {},
      notifyPanicRecover: (err) => setError(`Tailscale client stopped: ${err}`),
    })
  }

  return (
    <div>
      <PageHeader
        title={phase === 'form' ? 'SSH' : `SSH ${machineName}`}
        subtitle={
          <>
            Connects from this browser as a temporary tailnet node.{' '}
            <Link to="/machines" className="link">
              Back to machines
            </Link>
          </>
        }
      />

      <ErrorAlert error={error} />
      {error && (
        <p className="text-sm text-base-content/60 mb-4">
          The machine must run <code>tailscale up --ssh</code>, and your{' '}
          <Link to="/access-control" className="link">
            access control policy
          </Link>{' '}
          must allow SSH from you to it.
        </p>
      )}

      {phase === 'form' && (
        <form onSubmit={connect} className="flex flex-wrap items-end gap-3">
          <label className="form-control">
            <span className="label-text text-sm">Username on the machine</span>
            <input
              className="input input-bordered"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoFocus
            />
          </label>
          <button type="submit" className="btn btn-primary" disabled={!username.trim()}>
            <TerminalSquare className="size-4" />
            Connect
          </button>
        </form>
      )}

      {phase === 'connecting' && (
        <div className="flex items-center gap-3 text-sm text-base-content/70">
          <span className="loading loading-spinner" />
          {progress || 'Connecting...'}
        </div>
      )}

      {phase === 'connected' && (
        <>
          {progress && <p className="text-sm text-base-content/60 mb-2">{progress}</p>}
          <div ref={termRef} className="h-[70vh] rounded-box bg-black p-2" />
        </>
      )}

      {phase === 'done' && (
        <div className="flex items-center gap-3">
          <span className="text-sm text-base-content/70">Session ended.</span>
          <button className="btn btn-sm" onClick={() => window.location.reload()}>
            Reconnect
          </button>
        </div>
      )}
    </div>
  )
}
