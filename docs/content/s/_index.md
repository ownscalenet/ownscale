---
title: Short links
build:
  list: false
  render: never
---

This section holds short redirect links served at `/s/<slug>` (e.g.
`/s/agent`). To add one, create `site/content/s/<slug>.md` with:

```
---
title: "<slug>"
redirect_to: "/docs/some-page/#optional-anchor"
---
```

See `site/layouts/s/single.html` for the redirect template.
