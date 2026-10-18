// lib/faq.ts: link to FAQ entries on the docs site, which is served at the
// bare base domain (the console app itself lives at console.<baseDomain>).

// faqURL builds a link to the FAQ entry identified by anchor.
export function faqURL(anchor: string): string {
  const baseDomain = window.location.hostname.replace(/^console\./, '')
  return `https://${baseDomain}/docs/faqs/#${anchor}`
}

// shortURL builds a link to the docs site's short redirect page for slug,
// e.g. shortURL('agent') -> https://<baseDomain>/s/agent.
export function shortURL(slug: string): string {
  const baseDomain = window.location.hostname.replace(/^console\./, '')
  return `https://${baseDomain}/s/${slug}`
}
