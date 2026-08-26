package server

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder wraps an http.ResponseWriter to capture the status code
// written by the wrapped handler, defaulting to 200 OK if WriteHeader is
// never called explicitly (matching net/http's own behavior).
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// loggingMiddleware logs one line per HTTP request: method, path, status
// code, remote address, and duration.
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"remote_addr", r.RemoteAddr,
			"duration", time.Since(start),
		)
	})
}
