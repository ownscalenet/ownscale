// isSafeRelativeNext guards against open redirects: only a same-origin,
// path-absolute URL (no scheme-relative "//host" tricks) is followed.
export function isSafeRelativeNext(next: string | null): next is string {
  return !!next && next.startsWith('/') && !next.startsWith('//')
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
