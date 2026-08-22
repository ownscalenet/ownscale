# site/ — ownscale documentation portal

This is a [Hugo](https://gohugo.io) site using the
[hugo-book](https://github.com/alex-shpak/hugo-book) theme, imported as a
[Hugo Module](https://gohugo.io/hugo-modules/) (see `go.mod` / `go.sum`).
It is built and served as the landing page at `{$BASE_DOMAIN}` by Caddy
(see `etc/caddy/Caddyfile`).

## Editing docs

Add or edit Markdown files under `content/docs/`. `content/_index.md` is
the site's home page.

## Building

From the repository root, run:

```sh
make docs
```

This runs Hugo (`hugo --source site --destination ../etc/caddy/site
--minify`), regenerating `etc/caddy/site/`. That output is committed to
the repo and served directly by Caddy, so **commit the regenerated
`etc/caddy/site/` alongside your `site/` changes**.

Requires `go` on `PATH` so Hugo Modules can resolve the `hugo-book` theme
(cached locally after the first build; the module cache itself is not
committed).
