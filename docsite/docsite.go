// Package docsite serves the documentation site, built by Hugo into dist
// (see the Makefile's "docs" target), and the content domain's landing page.
package docsite

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/ownscalenet/ownscale/utils"
)

//go:embed all:dist
var distFS embed.FS

// csp allows the inline scripts and style attributes Hugo's theme uses.
const csp = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; object-src 'none'; base-uri 'self'"

// fingerprinted matches assets Hugo names after their content hash, which
// never change under the same name.
var fingerprinted = regexp.MustCompile(`\.[0-9a-f]{64}\.(js|css|json)$`)

// Handler returns an http.Handler serving the embedded docs site. Links to
// /console/<path> redirect to that path on console.baseDomain. When
// development is true, every response gets "Cache-Control: no-store".
func Handler(baseDomain string, development bool) http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("docsite: " + err.Error())
	}
	h, err := newHandler(sub, "https://console."+baseDomain, development)
	if err != nil {
		panic("docsite: " + err.Error())
	}
	return h
}

func newHandler(fsys fs.FS, consoleOrigin string, development bool) (http.Handler, error) {
	etags, err := utils.ContentETags(fsys)
	if err != nil {
		return nil, err
	}
	// Missing until `make docs` ran.
	notFound, err := fs.ReadFile(fsys, "404.html")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		if development {
			h.Set("Cache-Control", "no-store")
		} else {
			h.Set("Cache-Control", "no-cache")
		}

		// The docs are built without knowing the console's domain.
		if rest, ok := consolePath(r.URL.Path); ok {
			u := *r.URL
			u.Path = rest
			http.Redirect(w, r, consoleOrigin+u.RequestURI(), http.StatusFound)
			return
		}

		name, redirect, ok := resolve(etags, r.URL.Path)
		switch {
		case redirect:
			u := *r.URL
			u.Path = name
			http.Redirect(w, r, u.RequestURI(), http.StatusMovedPermanently)
		case ok:
			if !development && fingerprinted.MatchString(name) {
				h.Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			h.Set("ETag", etags[name])
			http.ServeFileFS(w, r, fsys, name)
		case notFound != nil:
			h.Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			if r.Method != http.MethodHead {
				_, _ = w.Write(notFound)
			}
		default:
			http.NotFound(w, r)
		}
	}), nil
}

// consolePath returns the console path that a docs path under /console
// links to.
func consolePath(urlPath string) (string, bool) {
	if urlPath == "/console" {
		return "/", true
	}
	rest, ok := strings.CutPrefix(urlPath, "/console/")
	return "/" + rest, ok
}

// resolve maps urlPath to a file in etags, serving a directory's index.html
// like Hugo's pretty URLs. A directory requested without its trailing slash
// gets redirect set, with name the URL path to redirect to. Unlike
// http.FileServer it never lists directories.
func resolve(etags map[string]string, urlPath string) (name string, redirect, ok bool) {
	clean := path.Clean("/" + urlPath)
	file := strings.TrimPrefix(clean, "/")
	if _, ok := etags[file]; ok {
		return file, false, true
	}
	index := path.Join(file, "index.html")
	if _, ok := etags[index]; !ok {
		return "", false, false
	}
	if !strings.HasSuffix(urlPath, "/") {
		return strings.TrimSuffix(clean, "/") + "/", true, false
	}
	return index, false, true
}

//go:embed landing.html
var landingHTML string

var landingTmpl = template.Must(template.New("landing").Parse(landingHTML))

// LandingHandler returns an http.Handler serving the page that explains
// what contentDomain is used for and who runs it, at baseDomain.
func LandingHandler(baseDomain, contentDomain string) http.Handler {
	var buf bytes.Buffer
	err := landingTmpl.Execute(&buf, struct{ BaseDomain, ContentDomain string }{baseDomain, contentDomain})
	if err != nil {
		panic("docsite: " + err.Error())
	}
	page := buf.Bytes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
		if r.Method != http.MethodHead {
			_, _ = w.Write(page)
		}
	})
}
