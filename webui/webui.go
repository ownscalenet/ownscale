// Package webui embeds and serves the built React frontend (frontend/dist),
// produced by the Vite build (see the frontend/ directory and the
// Makefile's "frontend" target). It serves static assets directly and falls
// back to index.html for any other path, so client-side routes (e.g.
// /machines) work on a full page load or refresh.
package webui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"

	"tailscale.com/tsweb/compserve"

	"github.com/ownscalenet/ownscale/utils"
)

//go:embed all:dist
var distFS embed.FS

// wasmPath is the Tailscale WebAssembly client the browser SSH page loads.
// Only its zstd copy (wasmPath + ".zst") is embedded, because the raw module
// is 27MB. Clients that don't accept zstd are served it decompressed.
const wasmPath = "assets/main.wasm"

// Handler returns an http.Handler serving the embedded frontend build. When
// development is true, every response gets "Cache-Control: no-store";
// otherwise files must be revalidated with their ETag, because asset URLs
// carry no version.
func Handler(development bool) http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist is embedded at build time; this can only fail if the "dist"
		// directory is missing from the embed, which would be a packaging bug.
		panic("webui: " + err.Error())
	}
	etags, err := utils.ContentETags(sub)
	if err != nil {
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
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		if assetPath(r.URL.Path) == wasmPath {
			serveWasm(w, r, sub, etags)
			return
		}

		// The embedded files have no modification time, so without an ETag
		// http.FileServer can't answer revalidations with 304.
		if tag, ok := etags[assetPath(r.URL.Path)]; ok {
			w.Header().Set("ETag", tag)
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html so client-side routing can take
		// over for any path that isn't a real static asset.
		w.Header().Set("ETag", etags["index.html"])
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

// serveWasm answers the Tailscale WebAssembly module from the embedded zstd
// copy, passed through to clients that accept zstd and decompressed for the
// rest.
func serveWasm(w http.ResponseWriter, r *http.Request, sub fs.FS, etags map[string]string) {
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("ETag", etags[wasmPath+compserve.Zstd.Ext])
	err := compserve.ServeFile(w, r, sub, wasmPath, compserve.Options{
		Encodings: []compserve.Encoding{compserve.Zstd},
	})
	if err == nil {
		return
	}
	w.Header().Del("ETag")
	w.Header().Del("Content-Encoding")
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
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
