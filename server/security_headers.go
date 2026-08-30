package server

import "net/http"

// securityCSP is intentionally strict: the built SPA (webui/dist) has no
// inline <script>/<style> tags and loads no external CDN assets/fonts, so
// nothing besides 'self' is needed for most directives. img-src is relaxed
// to also allow data: URIs because daisyUI's loading-spinner/skeleton
// components render inline SVGs as data: URI background-images.
const securityCSP = "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; object-src 'none'; base-uri 'self'"

// securityHeadersMiddleware sets a small set of hardening response headers
// on every response (both the /api/* JSON surface and the static SPA):
// nosniff blocks MIME-sniffing, the CSP and X-Frame-Options together
// prevent the console from ever being framed by another origin.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", securityCSP)
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
