import { useEffect, useState, type ReactNode } from 'react'

// CopyButton copies text to the clipboard and briefly says whether it worked.
// Copying fails on plain-HTTP origins, where the clipboard isn't available.
export default function CopyButton({
  text,
  className = 'btn',
  children,
}: {
  text: string
  className?: string
  children: ReactNode
}) {
  const [result, setResult] = useState<'copied' | 'failed' | null>(null)

  useEffect(() => {
    if (!result) return
    const timer = setTimeout(() => setResult(null), 2000)
    return () => clearTimeout(timer)
  }, [result])

  const copy = () => {
    // navigator.clipboard is undefined on insecure origins.
    Promise.resolve()
      .then(() => navigator.clipboard.writeText(text))
      .then(
        () => setResult('copied'),
        () => setResult('failed'),
      )
  }

  return (
    <button type="button" className={className} onClick={copy}>
      {result === 'copied' ? 'Copied!' : result === 'failed' ? 'Copy failed' : children}
    </button>
  )
}
