package server

import (
	"net/http"
	"strings"
)

// securityCSP is intentionally strict: the built SPA (webui/dist) has no
// inline <script>/<style> tags and loads no external CDN assets/fonts, so
// nothing besides 'self' is needed for most directives. img-src is relaxed
// to also allow data: URIs because daisyUI's loading-spinner/skeleton
// components render inline SVGs as data: URI background-images.
const securityCSP = "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; object-src 'none'; base-uri 'self'"

// sshCSP is securityCSP relaxed for the browser SSH page only:
// 'wasm-unsafe-eval' lets the Tailscale WebAssembly client be compiled,
// the wide connect-src is unavoidable because that client talks to the
// organization's headscale on a sibling subdomain and to whichever DERP
// relays the DERP map names, and xterm.js sizes its terminal through a
// <style> element it injects, which needs 'unsafe-inline'.
const sshCSP = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; connect-src 'self' https: wss:; img-src 'self' data:; frame-ancestors 'none'; object-src 'none'; base-uri 'self'"

// isSSHPath reports whether path is the console's browser SSH page
// ("/machines/{id}/ssh"), which the SPA serves as its own document.
func isSSHPath(path string) bool {
	rest, ok := strings.CutPrefix(path, "/machines/")
	if !ok {
		return false
	}
	id, ok := strings.CutSuffix(rest, "/ssh")
	return ok && id != "" && !strings.Contains(id, "/")
}

// securityHeadersMiddleware sets a small set of hardening response headers
// on every response (both the /api/* JSON surface and the static SPA):
// nosniff blocks MIME-sniffing, the CSP and X-Frame-Options together
// prevent the console from ever being framed by another origin.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if isSSHPath(r.URL.Path) {
			h.Set("Content-Security-Policy", sshCSP)
		} else {
			h.Set("Content-Security-Policy", securityCSP)
		}
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// hstsHeader covers subdomains too: org and funnel hosts are HTTPS-only.
const hstsHeader = "max-age=31536000; includeSubDomains"

func hstsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", hstsHeader)
		next.ServeHTTP(w, r)
	})
}
