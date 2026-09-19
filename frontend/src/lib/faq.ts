// lib/faq.ts: link to FAQ entries on the docs site, which is served at the
// bare base domain (the console app itself lives at console.<baseDomain>).

// faqURL builds a link to the FAQ entry identified by anchor.
export function faqURL(anchor: string): string {
  const baseDomain = window.location.hostname.replace(/^console\./, '')
  return `https://${baseDomain}/docs/faqs/#${anchor}`
}
