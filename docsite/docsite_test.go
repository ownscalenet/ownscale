package docsite

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const testConsole = "https://console.example.com"

func TestHandler(t *testing.T) {
	site := fstest.MapFS{
		"index.html":      {Data: []byte("home")},
		"404.html":        {Data: []byte("not here")},
		"docs/index.html": {Data: []byte("docs")},
		"css/site.css":    {Data: []byte("body{}")},
	}
	tests := []struct {
		name     string
		fsys     fstest.MapFS
		method   string
		path     string
		wantCode int
		wantBody string
		wantLoc  string
	}{
		{"home", site, http.MethodGet, "/", http.StatusOK, "home", ""},
		{"section", site, http.MethodGet, "/docs/", http.StatusOK, "docs", ""},
		{"section without slash", site, http.MethodGet, "/docs?q=1", http.StatusMovedPermanently, "", "/docs/?q=1"},
		{"asset", site, http.MethodGet, "/css/site.css", http.StatusOK, "body{}", ""},
		{"head", site, http.MethodHead, "/", http.StatusOK, "", ""},
		{"no directory listing", site, http.MethodGet, "/css/", http.StatusNotFound, "not here", ""},
		{"missing", site, http.MethodGet, "/nope", http.StatusNotFound, "not here", ""},
		{"traversal", site, http.MethodGet, "/../docs/", http.StatusBadRequest, "invalid URL path\n", ""},
		{"missing without 404 page", fstest.MapFS{"index.html": {Data: []byte("home")}}, http.MethodGet, "/nope", http.StatusNotFound, "404 page not found\n", ""},
		{"post", site, http.MethodPost, "/", http.StatusMethodNotAllowed, "Method Not Allowed\n", ""},
		{"console page", site, http.MethodGet, "/console/access-control?x=1", http.StatusFound, "", testConsole + "/access-control?x=1"},
		{"console home", site, http.MethodGet, "/console", http.StatusFound, "", testConsole + "/"},
		{"console home with slash", site, http.MethodGet, "/console/", http.StatusFound, "", testConsole + "/"},
		{"console nested", site, http.MethodHead, "/console/machines/5/ssh", http.StatusFound, "", testConsole + "/machines/5/ssh"},
		{"console lookalike", site, http.MethodGet, "/consolex", http.StatusNotFound, "not here", ""},
		{"console post", site, http.MethodPost, "/console/dns", http.StatusMethodNotAllowed, "Method Not Allowed\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := newHandler(tt.fsys, testConsole, false)
			if err != nil {
				t.Fatalf("newHandler: %v", err)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if got := rec.Body.String(); tt.wantLoc == "" && got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
			if got := rec.Header().Get("Location"); got != tt.wantLoc {
				t.Errorf("Location = %q, want %q", got, tt.wantLoc)
			}
			if tt.wantCode != http.StatusMethodNotAllowed && rec.Header().Get("Content-Security-Policy") != csp {
				t.Errorf("Content-Security-Policy = %q, want the docs CSP", rec.Header().Get("Content-Security-Policy"))
			}
		})
	}
}

func TestHandlerCacheControl(t *testing.T) {
	const hashed = "js/main.min.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.js"
	fsys := fstest.MapFS{
		"index.html": {Data: []byte("home")},
		hashed:       {Data: []byte("x")},
	}
	tests := []struct {
		name        string
		development bool
		path        string
		want        string
	}{
		{"page", false, "/", "no-cache"},
		{"fingerprinted asset", false, "/" + hashed, "public, max-age=31536000, immutable"},
		{"fingerprinted asset in development", true, "/" + hashed, "no-store"},
		{"missing", false, "/nope.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.js", "no-cache"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := newHandler(fsys, testConsole, tt.development)
			if err != nil {
				t.Fatalf("newHandler: %v", err)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if got := rec.Header().Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandlerRevalidation(t *testing.T) {
	h, err := newHandler(fstest.MapFS{"css/site.css": {Data: []byte("body{}")}}, testConsole, false)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/css/site.css", nil))
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	req := httptest.NewRequest(http.MethodGet, "/css/site.css", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("revalidation status = %d, want %d", rec.Code, http.StatusNotModified)
	}
}

func TestLandingHandler(t *testing.T) {
	h := LandingHandler("example.com", "example-content.net")
	tests := []struct {
		path     string
		wantCode int
	}{
		{"/", http.StatusOK},
		{"/index.html", http.StatusOK},
		{"/other", http.StatusNotFound},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if rec.Code != tt.wantCode {
			t.Errorf("GET %s: status = %d, want %d", tt.path, rec.Code, tt.wantCode)
		}
		if tt.wantCode != http.StatusOK {
			continue
		}
		body := rec.Body.String()
		for _, want := range []string{"example.com", "example-content.net"} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s: body doesn't mention %q", tt.path, want)
			}
		}
	}
}
