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
	h := Handler()

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
	h := Handler()

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
	h := Handler()

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
