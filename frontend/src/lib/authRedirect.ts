// isSafeRelativeNext guards against open redirects: only a same-origin,
// path-absolute URL is followed. Browsers read a backslash as a slash, so
// "//host" and "/\host" are both rejected, like control characters. Keep in
// sync with isSafeNext in handlers/auth.go.
export function isSafeRelativeNext(next: string | null): next is string {
  if (!next || !next.startsWith('/') || next.startsWith('//')) return false
  return ![...next].some((c) => c === '\\' || c.charCodeAt(0) < 0x20 || c.charCodeAt(0) === 0x7f)
}

// goToNext makes the same next-or-home redirect as handlers.finishLogin. The
// passkey finish endpoints redirect only the fetch(), not the browser.
export function goToNext(next: string | null) {
  window.location.assign(isSafeRelativeNext(next) ? next : '/')
}

// nextQuery returns "?next=..." for next, or "" when there is none.
export function nextQuery(next: string | null): string {
  return next ? `?next=${encodeURIComponent(next)}` : ''
}
