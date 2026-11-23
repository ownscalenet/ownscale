// lib/faq.ts: link to the docs site, which is served at the bare base
// domain (the console app itself lives at console.<baseDomain>).

function baseDomain(): string {
  return window.location.hostname.replace(/^console\./, '')
}

// siteURL is the docs site's home page.
export function siteURL(): string {
  return `https://${baseDomain()}/`
}

// docsURL is the documentation's start page.
export function docsURL(): string {
  return `https://${baseDomain()}/docs/`
}

// faqURL builds a link to the FAQ entry identified by anchor.
export function faqURL(anchor: string): string {
  return `https://${baseDomain()}/docs/faqs/#${anchor}`
}

// shortURL builds a link to the docs site's short redirect page for slug,
// e.g. shortURL('agent') -> https://<baseDomain>/s/agent.
export function shortURL(slug: string): string {
  return `https://${baseDomain()}/s/${slug}`
}

// docPageURL builds a link to a docs page under /docs/, optionally to an
// anchor within it, e.g. docPageURL('use-cases/funnels') or
// docPageURL('reference/terminology', 'auth-key').
export function docPageURL(path: string, anchor?: string): string {
  return `https://${baseDomain()}/docs/${path}/${anchor ? `#${anchor}` : ''}`
}
