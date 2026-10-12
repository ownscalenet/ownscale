package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssetPath(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "index.html"},
		{"/", "index.html"},
		{"/assets/x.js", "assets/x.js"},
		{"/favicon.svg", "favicon.svg"},
		{"no-leading-slash", "no-leading-slash"},
	}
	for _, tt := range tests {
		if got := assetPath(tt.in); got != tt.want {
			t.Errorf("assetPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHandlerServesIndexAtRoot(t *testing.T) {
	h := Handler(false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("body doesn't look like index.html: %q", rec.Body.String())
	}
}

func TestHandlerServesRealAsset(t *testing.T) {
	h := Handler(false)

	req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() == 0 {
		t.Error("body is empty, want the real favicon contents")
	}
}

func TestHandlerRejectsNonGetHead(t *testing.T) {
	h := Handler(false)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandlerCacheControl(t *testing.T) {
	cases := []struct {
		name        string
		development bool
		want        string
	}{
		{"production omits header", false, ""},
		{"development disables caching", true, "no-store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := Handler(tc.development)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if got := rec.Header().Get("Cache-Control"); got != tc.want {
				t.Errorf("Cache-Control = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHandlerServesWasm covers the Tailscale WebAssembly module, which is
// only shipped precompressed. It is copied into dist by the Makefile's
// "frontend" target, so skip when the frontend hasn't been built.
func TestHandlerServesWasm(t *testing.T) {
	if _, err := distFS.ReadFile("dist/" + wasmPath + ".gz"); err != nil {
		t.Skip("frontend not built: no embedded wasm module")
	}
	h := Handler(false)

	tests := []struct {
		name         string
		accept       string
		wantCode     int
		wantEncoding string
	}{
		{"brotli preferred", "br, gzip", http.StatusOK, "br"},
		{"gzip only", "gzip", http.StatusOK, "gzip"},
		{"no supported encoding", "identity", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/"+wasmPath, nil)
			req.Header.Set("Accept-Encoding", tt.accept)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if got := rec.Header().Get("Content-Encoding"); got != tt.wantEncoding {
				t.Errorf("Content-Encoding = %q, want %q", got, tt.wantEncoding)
			}
			if tt.wantCode != http.StatusOK {
				return
			}
			if got := rec.Header().Get("Content-Type"); got != "application/wasm" {
				t.Errorf("Content-Type = %q, want application/wasm", got)
			}
			if rec.Header().Get("ETag") == "" {
				t.Error("ETag is empty")
			}
			if rec.Body.Len() == 0 {
				t.Error("body is empty")
			}
		})
	}
}
