import { Download } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useAuth } from '../authContext'
import { docPageURL } from '../lib/faq'
import { CopyIcon } from './CopyButton'
import Modal from './Modal'

const platforms = [
  { id: 'windows', label: 'Windows', download: 'https://tailscale.com/download/windows' },
  { id: 'macos', label: 'macOS', download: 'https://tailscale.com/download/mac' },
  { id: 'linux', label: 'Linux', download: 'https://tailscale.com/download/linux' },
  { id: 'ios', label: 'iOS', download: 'https://apps.apple.com/app/tailscale/id1470499037' },
  {
    id: 'android',
    label: 'Android',
    download: 'https://play.google.com/store/apps/details?id=com.tailscale.ipn',
  },
] as const

type PlatformID = (typeof platforms)[number]['id']

// detectPlatform preselects the browser's own OS, since most people add
// the device they're on.
function detectPlatform(): PlatformID {
  const ua = navigator.userAgent
  if (/iPhone|iPad|iPod/.test(ua)) return 'ios'
  if (/Android/.test(ua)) return 'android'
  if (/Mac/.test(ua)) return 'macos'
  if (/Linux|X11/.test(ua)) return 'linux'
  return 'windows'
}

function CodeBlock({ text, label }: { text: string; label: string }) {
  return (
    <div className="relative">
      <div className="mockup-code text-xs overflow-x-auto">
        <pre data-prefix="$">
          <code>{text}</code>
        </pre>
      </div>
      {/* The code block is neutral in every theme; the ghost button colors itself with base-content. */}
      <span className="absolute top-2 right-2 [--color-base-content:var(--color-neutral-content)]">
        <CopyIcon text={text} label={label} />
      </span>
    </div>
  )
}

function Step({ n, children }: { n: number; children: ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="badge badge-neutral badge-sm mt-0.5 shrink-0">{n}</span>
      <div className="flex-1 min-w-0 space-y-2 text-sm">{children}</div>
    </li>
  )
}

// ClientDeviceDialog shows how to add a laptop or phone for one OS at a
// time; the device joins by signing in, so it's owned by whoever signs in.
export default function ClientDeviceDialog({ onClose }: { onClose: () => void }) {
  const [platform, setPlatform] = useState<PlatformID>(detectPlatform)
  const { organization } = useAuth()
  const loginServer = `${window.location.protocol}//${organization?.Domain}`
  const command = `tailscale up --login-server=${loginServer}`
  const p = platforms.find((x) => x.id === platform)!

  const loginServerField = (
    <div className="flex items-center gap-1 rounded-field bg-base-200 px-3 py-1.5">
      <code className="flex-1 truncate text-xs">{loginServer}</code>
      <CopyIcon text={loginServer} label="login server" />
    </div>
  )

  return (
    <Modal open onClose={onClose} title="Add client device">
      <div role="tablist" className="tabs tabs-box tabs-sm mb-4 w-fit">
        {platforms.map((x) => (
          <button
            key={x.id}
            type="button"
            role="tab"
            aria-selected={platform === x.id}
            className={`tab ${platform === x.id ? 'tab-active' : ''}`}
            onClick={() => setPlatform(x.id)}
          >
            {x.label}
          </button>
        ))}
      </div>

      <ol className="space-y-4">
        <Step n={1}>
          <a className="btn btn-sm btn-outline" href={p.download} target="_blank" rel="noreferrer">
            <Download className="size-3.5" aria-hidden="true" />
            Download Tailscale for {p.label}
          </a>
        </Step>
        {(platform === 'windows' || platform === 'linux') && (
          <Step n={2}>
            <p>
              {platform === 'windows'
                ? 'Open a Command Prompt or PowerShell and run:'
                : 'In a terminal, run:'}
            </p>
            <CodeBlock text={platform === 'linux' ? `sudo ${command}` : command} label="command" />
            <p className="opacity-60">Open the URL it prints and sign in.</p>
          </Step>
        )}
        {platform === 'macos' && (
          <Step n={2}>
            <p>
              Option-click the Tailscale menu bar icon, open <b>Debug</b>, and under{' '}
              <b>Custom Login Server</b> choose <b>Add Account…</b>. Enter this login server and
              sign in:
            </p>
            {loginServerField}
          </Step>
        )}
        {(platform === 'ios' || platform === 'android') && (
          <Step n={2}>
            <p>
              {platform === 'ios' ? (
                <>
                  Tap the account icon, then <b>Log in…</b>. In the top-right menu choose{' '}
                  <b>Use custom coordination server</b>, enter this login server and sign in:
                </>
              ) : (
                <>
                  Open <b>Settings</b>, then <b>Accounts</b>. In the ⋮ menu choose{' '}
                  <b>Use an alternate server</b>, enter this login server and sign in:
                </>
              )}
            </p>
            {loginServerField}
          </Step>
        )}
      </ol>

      <div className="modal-action items-center">
        <a
          className="link text-sm opacity-60 mr-auto"
          href={docPageURL('connect', platform)}
          target="_blank"
          rel="noreferrer"
        >
          More about {p.label}
        </a>
        <button type="button" className="btn btn-primary" onClick={onClose}>
          Done
        </button>
      </div>
    </Modal>
  )
}
