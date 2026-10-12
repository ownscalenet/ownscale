// Package webui embeds and serves the built React frontend (frontend/dist),
// produced by the Vite build (see the frontend/ directory and the
// Makefile's "frontend" target). It serves static assets directly and falls
// back to index.html for any other path, so client-side routes (e.g.
// /machines) work on a full page load or refresh.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed all:dist
var distFS embed.FS

// wasmPath is the Tailscale WebAssembly client the browser SSH page loads.
// Only its precompressed copies are shipped (the raw module is 27MB), so
// it is served straight from them, picked by the request's Accept-Encoding.
const wasmPath = "assets/main.wasm"

// wasmEncodings maps a Content-Encoding to the embedded file holding the
// module in that encoding, best first.
var wasmEncodings = []struct{ encoding, file string }{
	{"br", wasmPath + ".br"},
	{"gzip", wasmPath + ".gz"},
}

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

		if assetPath(r.URL.Path) == wasmPath {
			serveWasm(w, r, sub)
			return
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

// serveWasm answers the Tailscale WebAssembly module from the embedded
// precompressed copy matching the request's Accept-Encoding. A client that
// accepts neither encoding gets a 404: every browser that can run the
// module supports at least gzip.
func serveWasm(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	w.Header().Add("Vary", "Accept-Encoding")
	accept := r.Header.Get("Accept-Encoding")
	for _, e := range wasmEncodings {
		if !strings.Contains(accept, e.encoding) {
			continue
		}
		// TODO: better not to load the whole file into memory and
		// instead read twice.
		body, err := fs.ReadFile(sub, e.file)
		if err != nil {
			continue
		}
		w.Header().Set("Content-Type", "application/wasm")
		w.Header().Set("Content-Encoding", e.encoding)
		// The URL has no version in it, so the module must be revalidated;
		// the ETag keeps that cheap.
		w.Header().Set("ETag", wasmETag(e.file, body))
		if w.Header().Get("Cache-Control") == "" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
		return
	}
	http.NotFound(w, r)
}

// wasmETags caches the content hash of each embedded module copy, which
// doesn't change for the lifetime of the binary.
var wasmETags sync.Map

// wasmETag returns the ETag for the embedded file name holding body.
func wasmETag(name string, body []byte) string {
	if tag, ok := wasmETags.Load(name); ok {
		return tag.(string)
	}
	sum := sha256.Sum256(body)
	tag := `"` + hex.EncodeToString(sum[:8]) + `"`
	wasmETags.Store(name, tag)
	return tag
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
