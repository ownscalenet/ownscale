package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestConsoleRoutes(t *testing.T) {
	dir := t.TempDir()
	appTSX := `
<Routes>
  <Route path="/login" element={<Login />} />
  <Route path="/invite/:token" element={<AcceptInvite />} />
  <Route path="/" element={<Layout />}>
    <Route index element={<Navigate to="/machines" replace />} />
    <Route path="machines" element={<Machines />} />
    <Route path="machines/:ip" element={<MachineDetail />} />
  </Route>
  <Route path="*" element={<NotFound />} />
</Routes>
`
	path := filepath.Join(dir, "App.tsx")
	if err := os.WriteFile(path, []byte(appTSX), 0o644); err != nil {
		t.Fatal(err)
	}

	routes, err := consoleRoutes(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/login", "/invite/:token", "/", "/", "machines", "machines/:ip", "*"}
	if len(routes) != len(want) {
		t.Fatalf("consoleRoutes() = %v, want %v", routes, want)
	}
	for i := range want {
		if routes[i] != want[i] {
			t.Errorf("routes[%d] = %q, want %q", i, routes[i], want[i])
		}
	}
}

func TestMatchesRoute(t *testing.T) {
	routes := []string{"/login", "/invite/:token", "/", "machines", "machines/:ip", "*"}
	tests := []struct {
		path string
		want bool
	}{
		{"/login", true},
		{"/invite/abc123", true},
		{"", true},
		{"/", true},
		{"/machines", true},
		{"/machines/100.64.0.1", true},
		{"/machines/100.64.0.1/extra", false},
		{"/nope", false},
		{"/anything-at-all", false}, // "*" must not make every link valid
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := matchesRoute(routes, tt.path); got != tt.want {
				t.Errorf("matchesRoute(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFindDocLinks(t *testing.T) {
	dir := t.TempDir()
	src := `
import { docPageURL, faqURL, shortURL, docsURL, siteURL } from '../lib/faq'

function Page() {
  return (
    <>
      <a href={docPageURL('use-cases/funnels')} />
      <a href={docPageURL('reference/terminology', 'auth-key')} />
      <a href={faqURL('why-expired')} />
      <a href={shortURL('agent')} />
      <a href={docsURL()} />
      <a href={siteURL()} />
    </>
  )
}
`
	path := filepath.Join(dir, "Page.tsx")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	links, err := findDocLinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []docLink{
		{file: "docs/use-cases/funnels/index.html"},
		{file: "docs/reference/terminology/index.html", anchor: "auth-key"},
		{file: "docs/faqs/index.html", anchor: "why-expired"},
		{file: "s/agent/index.html"},
		{file: "docs/index.html"},
		{file: "index.html"},
	}
	if len(links) != len(want) {
		t.Fatalf("findDocLinks() = %+v, want %+v", links, want)
	}
	for i := range want {
		if links[i].file != want[i].file || links[i].anchor != want[i].anchor {
			t.Errorf("links[%d] = %+v, want %+v", i, links[i], want[i])
		}
		if !strings.Contains(links[i].source, "Page.tsx:") {
			t.Errorf("links[%d].source = %q, want it to mention Page.tsx", i, links[i].source)
		}
	}
}

func TestFindConsoleLinks(t *testing.T) {
	dir := t.TempDir()
	md := "See [Funnels](/console/funnels) and `/console/dns` and [home](/console/?x=1#y).\n"
	path := filepath.Join(dir, "page.md")
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	links, err := findConsoleLinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"/funnels": true, "/dns": true, "/": true}
	if len(links) != len(want) {
		t.Fatalf("findConsoleLinks() = %+v, want paths %v", links, want)
	}
	for _, l := range links {
		if !want[l.path] {
			t.Errorf("findConsoleLinks() returned unexpected path %q", l.path)
		}
	}
}

func TestDocLinkValidate(t *testing.T) {
	docsDist := fstest.MapFS{
		"docs/use-cases/funnels/index.html":     {Data: []byte(`<h1 id="top">Funnels</h1>`)},
		"docs/reference/terminology/index.html": {Data: []byte(`<h3 id="auth-key">Auth key</h3>`)},
		"docs/reference/minified/index.html":    {Data: []byte(`<h3 id=auth-key>Auth key</h3>`)},
	}
	tests := []struct {
		name    string
		link    docLink
		wantErr bool
	}{
		{"page exists, no anchor", docLink{file: "docs/use-cases/funnels/index.html"}, false},
		{"page missing", docLink{file: "docs/use-cases/nope/index.html"}, true},
		{"anchor exists, quoted", docLink{file: "docs/reference/terminology/index.html", anchor: "auth-key"}, false},
		{"anchor exists, unquoted (minified)", docLink{file: "docs/reference/minified/index.html", anchor: "auth-key"}, false},
		{"anchor missing", docLink{file: "docs/reference/terminology/index.html", anchor: "nope"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.link.validate(docsDist)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestRun builds a tiny fixture repo (App.tsx, a frontend page with a
// valid and a stale doc link, docs content with a valid and a stale
// console link, and a pre-"built" docs dist) and checks that run reports
// exactly the two stale links.
func TestRun(t *testing.T) {
	root := t.TempDir()
	frontendSrc := filepath.Join(root, "frontend", "src")
	docsContent := filepath.Join(root, "docs", "content")
	docsDist := filepath.Join(root, "docsite", "dist")
	for _, dir := range []string{frontendSrc, docsContent, docsDist} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	write := func(path, content string) {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("frontend/src/App.tsx", `
<Routes>
  <Route path="/" element={<Layout />}>
    <Route path="funnels" element={<Funnel />} />
  </Route>
</Routes>
`)
	write("frontend/src/pages/Funnel.tsx", `
<a href={docPageURL('use-cases/funnels')} />
<a href={docPageURL('use-cases/stale-page')} />
`)
	write("docs/content/docs/use-cases/funnels.md", "Open [Funnels](/console/funnels).\n")
	write("docs/content/docs/use-cases/broken.md", "Open [Users](/console/users).\n")
	write("docsite/dist/docs/use-cases/funnels/index.html", "<h1>Funnels</h1>")

	err := run(frontendSrc, docsContent, docsDist)
	if err == nil {
		t.Fatal("run() = nil, want an error listing the two stale links")
	}
	for _, want := range []string{"stale-page", `console route "/users" doesn't exist`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("run() error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "use-cases/funnels/index.html\" doesn't exist") {
		t.Errorf("run() reported the valid funnels link as stale: %v", err)
	}
	if strings.Contains(err.Error(), `"/funnels" doesn't exist`) {
		t.Errorf("run() reported the valid /console/funnels link as stale: %v", err)
	}
}
