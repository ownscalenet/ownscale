package server

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/handlers"
)

func TestNewMuxCompressionAndHSTS(t *testing.T) {
	page := strings.Repeat("<p>hello</p>", 100)
	tests := []struct {
		name         string
		development  bool
		method       string
		host         string
		acceptEnc    string
		wantEncoding string
		wantHSTS     bool
	}{
		{"console zstd", false, http.MethodGet, "console.example.com", "gzip, deflate, br, zstd", "zstd", true},
		{"console gzip", false, http.MethodGet, "console.example.com", "gzip", "gzip", true},
		{"console identity", false, http.MethodGet, "console.example.com", "", "", true},
		{"console head", false, http.MethodHead, "console.example.com", "gzip", "", true},
		{"docs", false, http.MethodGet, "example.com", "gzip", "gzip", true},
		{"landing", false, http.MethodGet, "example-content.net", "gzip", "gzip", true},
		{"development", true, http.MethodGet, "console.example.com", "gzip", "gzip", false},
		{"funnel", false, http.MethodGet, "web-acme.example-content.net", "gzip", "", false},
		{"org", false, http.MethodGet, "acme.example.com", "gzip", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, queries := dbtest.New(t)
			h, err := handlers.New(queries, &config.Config{
				BaseDomain:    "example.com",
				ContentDomain: "example-content.net",
				DataDir:       t.TempDir(),
				Development:   tt.development,
			}, nil, slog.New(slog.DiscardHandler), nil)
			if err != nil {
				t.Fatalf("handlers.New: %v", err)
			}
			t.Cleanup(func() { _ = h.Close(t.Context()) })
			static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = io.WriteString(w, page)
			})
			requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
				return auth.RequireAuthAPI(queries.Queries, next)
			}
			mux := NewMux(h, requireAuthAPI, slog.New(slog.DiscardHandler), static, nil)

			req := httptest.NewRequest(tt.method, "/", nil)
			req.Host = tt.host
			if tt.acceptEnc != "" {
				req.Header.Set("Accept-Encoding", tt.acceptEnc)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if got := rec.Header().Get("Content-Encoding"); got != tt.wantEncoding {
				t.Errorf("Content-Encoding = %q, want %q", got, tt.wantEncoding)
			}
			if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tt.wantHSTS {
				t.Errorf("Strict-Transport-Security set = %v, want %v", got, tt.wantHSTS)
			}
			if tt.wantEncoding == "gzip" && tt.host == "console.example.com" {
				zr, err := gzip.NewReader(rec.Body)
				if err != nil {
					t.Fatalf("gzip.NewReader: %v", err)
				}
				body, err := io.ReadAll(zr)
				if err != nil {
					t.Fatalf("read gzip body: %v", err)
				}
				if string(body) != page {
					t.Errorf("decompressed body = %q, want the page", body)
				}
			}
		})
	}
}
