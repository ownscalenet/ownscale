# docs/ — ownscale documentation portal

This is a [Hugo](https://gohugo.io) site using the
[Hextra](https://github.com/imfing/hextra) theme, imported as a
[Hugo Module](https://gohugo.io/hugo-modules/) (see `go.mod` / `go.sum`).
It is embedded into the ownscale binary (package `docsite`) and served at
`{$BASE_DOMAIN}`.

## Editing docs

`content/_index.md` is the site's home page. Pages live in sections under
`content/docs/`, modeled on Tailscale's and Headscale's docs:

| Section | Holds |
| --- | --- |
| `get-started/` | what ownscale is, quickstart, connecting clients |
| `concepts/` | how it works, terminology glossary, organizations, architecture |
| `features/` | one page per console feature: what, who can use it, limits |
| `self-hosting/` | requirements, installation, own reverse proxy |
| `reference/` | `osa` CLI, domains, limitations |
| `faqs.md` | FAQs |

Conventions:

- Order pages with `weight` in front matter; each section's `_index.md` has
  a cards overview.
- Link between pages with `relref`, so a broken link fails the build.
- Link to console pages with `/console/<path>`, e.g.
  `[Access control](/console/access-control)`. ownscale redirects these to
  `https://console.<BASE_DOMAIN>/<path>`; they don't work under `hugo server`.
- When moving a page, add its old URL to `aliases` in front matter.
- The console links to `/docs/faqs/#ownscale-agent-user--machine` (via
  `/s/agent`): keep that heading unchanged.
- Check facts against the code. Link to tailscale.com/kb for client and
  CLI details instead of copying them.

## Building

From the repository root, run:

```sh
make docs
```

This runs Hugo (`hugo --source docs --destination ../docsite/dist
--minify`), regenerating `docsite/dist/`, which `go:embed` picks up. In the
dev stack, follow it with `docker compose up -d --build ownscale`.

Requires `go` on `PATH` so Hugo Modules can resolve the `hextra` theme
(cached locally after the first build; the module cache itself is not
committed).
