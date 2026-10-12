package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := securityHeadersMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	tests := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"Content-Security-Policy", securityCSP},
		{"X-Frame-Options", "DENY"},
	}
	for _, tt := range tests {
		if got := rec.Header().Get(tt.header); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestSecurityHeadersSSHPath(t *testing.T) {
	mw := securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		path string
		want string
	}{
		{"/machines/5/ssh", sshCSP},
		{"/machines", securityCSP},
		{"/machines/5", securityCSP},
		{"/machines//ssh", securityCSP},
		{"/machines/5/ssh/more", securityCSP},
		{"/api/machines/5/ssh-session", securityCSP},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if got := rec.Header().Get("Content-Security-Policy"); got != tt.want {
				t.Errorf("Content-Security-Policy = %q, want %q", got, tt.want)
			}
		})
	}
}
