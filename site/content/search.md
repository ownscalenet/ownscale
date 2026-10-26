---
title: Search
bookSearchExclude: true
---

<div class="book-search">
  <input id="search-page-input" type="text" placeholder="Search the docs" maxlength="64" autofocus>
</div>
<p id="search-page-status"></p>
<ul id="search-page-results"></ul>

<style>
  #search-page-input {
    width: 100%;
    max-width: 30rem;
  }
  #search-page-status {
    opacity: 0.6;
    font-size: var(--font-size-smaller);
  }
  #search-page-results {
    max-width: 50rem;
  }
  #search-page-results li {
    margin-bottom: 1.25rem;
  }
  #search-page-results .search-page-section {
    opacity: 0.5;
  }
  #search-page-results .search-page-excerpt {
    margin: 0.25rem 0 0;
  }
  #search-page-results mark {
    background: var(--gray-200);
    color: inherit;
  }
</style>

<script type="module">
  import Fuse from '/fuse.min.mjs';

  const input = document.querySelector('#search-page-input');
  const status = document.querySelector('#search-page-status');
  const results = document.querySelector('#search-page-results');

  const indexConfig = {
    includeScore: true,
    includeMatches: true,
    useExtendedSearch: true,
    fieldNormWeight: 1.5,
    threshold: 0.2,
    ignoreLocation: true,
    keys: [
      { name: 'title', weight: 0.7 },
      { name: 'content', weight: 0.3 },
    ],
  };

  let fuse;
  const ready = fetch('/search-index.json')
    .then((res) => res.json())
    .then((pages) => {
      fuse = new Fuse(pages, indexConfig);
    });

  function excerpt(page, matches) {
    const contentMatch = (matches || []).find((m) => m.key === 'content');
    if (!contentMatch || !contentMatch.indices.length) {
      return page.content.slice(0, 160).trim() + '…';
    }
    const [start, end] = contentMatch.indices.reduce((best, span) =>
      span[1] - span[0] > best[1] - best[0] ? span : best
    );
    const from = Math.max(0, start - 60);
    const to = Math.min(page.content.length, end + 60);
    const prefix = from > 0 ? '…' : '';
    const suffix = to < page.content.length ? '…' : '';
    const before = page.content.slice(from, start);
    const match = page.content.slice(start, end + 1);
    const after = page.content.slice(end + 1, to);
    return `${prefix}${before}<mark>${match}</mark>${after}${suffix}`;
  }

  // exactQuery requires each word to appear as typed. Fuzzy matching over
  // whole pages matches scattered letters (e.g. "still" for "split"), and
  // reports them as matches, which the excerpt would highlight. So it's only
  // the fallback, for typos.
  function exactQuery(term) {
    return term.split(/\s+/).map((word) => `'${word}`).join(' ');
  }

  function render(term) {
    results.replaceChildren();
    if (!term) {
      status.textContent = '';
      return;
    }
    let hits = fuse.search(exactQuery(term));
    const fuzzy = !hits.length;
    if (fuzzy) hits = fuse.search(term);
    hits = hits.slice(0, 20);
    const count = `${hits.length} result${hits.length === 1 ? '' : 's'}`;
    status.textContent = !hits.length
      ? `No results for "${term}"`
      : fuzzy
        ? `No exact matches for "${term}"; ${count} for similar words`
        : `${count} for "${term}"`;
    for (const hit of hits) {
      const li = document.createElement('li');
      const a = document.createElement('a');
      a.href = hit.item.href;
      a.textContent = hit.item.title;
      const section = document.createElement('div');
      section.className = 'search-page-section';
      section.textContent = hit.item.section;
      const p = document.createElement('p');
      p.className = 'search-page-excerpt';
      p.innerHTML = excerpt(hit.item, hit.matches);
      li.append(a, section, p);
      results.append(li);
    }
  }

  function search() {
    const term = input.value.trim();
    const url = new URL(location.href);
    if (term) {
      url.searchParams.set('q', term);
    } else {
      url.searchParams.delete('q');
    }
    history.replaceState(null, '', url);
    ready.then(() => render(term));
  }

  input.addEventListener('input', search);

  const initialQuery = new URLSearchParams(location.search).get('q');
  if (initialQuery) {
    input.value = initialQuery;
  }
  search();
</script>
