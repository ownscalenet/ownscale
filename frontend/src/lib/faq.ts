// lib/faq.ts: link to the docs site, which is served at the bare base
// domain (the console app itself lives at console.<baseDomain>).

function baseDomain(): string {
  return window.location.hostname.replace(/^console\./, '')
}

// siteURL is the docs site's home page.
export function siteURL(): string {
  return `https://${baseDomain()}/`
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
