// Package webui embeds and serves the built React frontend (frontend/dist),
// produced by the Vite build (see the frontend/ directory and the
// Makefile's "frontend" target). It serves static assets directly and falls
// back to index.html for any other path, so client-side routes (e.g.
// /machines) work on a full page load or refresh.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler serving the embedded frontend build. When
// development is true, every response gets "Cache-Control: no-store".
func Handler(development bool) http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist is embedded at build time; this can only fail if the "dist"
		// directory is missing from the embed, which would be a packaging bug.
		panic("webui: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		if development {
			w.Header().Set("Cache-Control", "no-store")
		}

		if _, err := fs.Stat(sub, assetPath(r.URL.Path)); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html so client-side routing can take
		// over for any path that isn't a real static asset.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

// assetPath converts a URL path into a path relative to the embedded FS
// root (fs.FS paths must not have a leading slash).
func assetPath(urlPath string) string {
	if urlPath == "" || urlPath == "/" {
		return "index.html"
	}
	if urlPath[0] == '/' {
		return urlPath[1:]
	}
	return urlPath
}
