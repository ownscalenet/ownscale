// safeNextPath guards against open redirects: it returns next as a
// same-origin path, or null. Parsing against the page origin handles browsers
// reading a backslash as a slash ("/\host" resolves to another origin).
// Keep in sync with isSafeNext in handlers/auth.go.
export function safeNextPath(next: string | null): string | null {
  if (!next || !next.startsWith('/')) return null
  const url = new URL(next, window.location.origin)
  if (url.origin !== window.location.origin) return null
  return url.pathname + url.search + url.hash
}

// goToNext makes the same next-or-home redirect as handlers.finishLogin. The
// passkey finish endpoints redirect only the fetch(), not the browser.
export function goToNext(next: string | null) {
  window.location.assign(safeNextPath(next) ?? '/')
}

// nextQuery returns "?next=..." for next, or "" when there is none.
export function nextQuery(next: string | null): string {
  return next ? `?next=${encodeURIComponent(next)}` : ''
}
