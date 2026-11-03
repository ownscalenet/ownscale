package webui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tailscale.com/util/zstdframe"
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
		{"production requires revalidation", false, "no-cache"},
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

func TestHandlerRevalidation(t *testing.T) {
	h := Handler(false)

	tests := []struct {
		name string
		path string
	}{
		{"index", "/"},
		{"real asset", "/favicon.svg"},
		{"spa fallback", "/machines"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			etag := rec.Header().Get("ETag")
			if etag == "" {
				t.Fatal("ETag is empty")
			}

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("If-None-Match", etag)
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotModified {
				t.Errorf("revalidation status = %d, want %d", rec.Code, http.StatusNotModified)
			}
		})
	}
}

// TestHandlerServesWasm covers the Tailscale WebAssembly module, which is
// only embedded zstd-compressed. It is copied into dist by the Makefile's
// "frontend" target, so skip when the frontend hasn't been built.
func TestHandlerServesWasm(t *testing.T) {
	compressed, err := distFS.ReadFile("dist/" + wasmPath + ".zst")
	if err != nil {
		t.Skip("frontend not built: no embedded wasm module")
	}
	raw, err := zstdframe.AppendDecode(nil, compressed)
	if err != nil {
		t.Fatalf("decoding embedded wasm: %v", err)
	}
	h := Handler(false)

	tests := []struct {
		name         string
		accept       string
		wantEncoding string
		wantBody     []byte
	}{
		{"zstd", "gzip, deflate, br, zstd", "zstd", compressed},
		{"no zstd", "gzip, br", "", raw},
		{"no Accept-Encoding", "", "", raw},
		{"zstd rejected", "zstd;q=0, gzip", "", raw},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/"+wasmPath, nil)
			if tt.accept != "" {
				req.Header.Set("Accept-Encoding", tt.accept)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get("Content-Encoding"); got != tt.wantEncoding {
				t.Errorf("Content-Encoding = %q, want %q", got, tt.wantEncoding)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/wasm" {
				t.Errorf("Content-Type = %q, want application/wasm", got)
			}
			if rec.Header().Get("ETag") == "" {
				t.Error("ETag is empty")
			}
			if !bytes.Equal(rec.Body.Bytes(), tt.wantBody) {
				t.Errorf("body differs: got %d bytes, want %d", rec.Body.Len(), len(tt.wantBody))
			}
		})
	}
}
