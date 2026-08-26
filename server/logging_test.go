package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoggingMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	mw := loggingMiddleware(logger, next)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v; raw=%s", err, buf.String())
	}
	if entry["method"] != http.MethodPost {
		t.Errorf("method = %v, want %q", entry["method"], http.MethodPost)
	}
	if entry["path"] != "/api/organizations" {
		t.Errorf("path = %v, want %q", entry["path"], "/api/organizations")
	}
	if status, ok := entry["status"].(float64); !ok || int(status) != http.StatusCreated {
		t.Errorf("status = %v, want %d", entry["status"], http.StatusCreated)
	}
	if _, ok := entry["duration"]; !ok {
		t.Error("expected a duration field in the log entry")
	}
}

func TestLoggingMiddlewareDefaultStatusOK(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never calls WriteHeader explicitly.
		_, _ = w.Write([]byte("ok"))
	})
	mw := loggingMiddleware(logger, next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if status, ok := entry["status"].(float64); !ok || int(status) != http.StatusOK {
		t.Errorf("status = %v, want %d (default)", entry["status"], http.StatusOK)
	}
}
