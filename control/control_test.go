package control

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
)

func TestDeleteOrganization(t *testing.T) {
	tests := []struct {
		name      string
		serve     bool
		deleteErr error
		wantErr   bool
		wantIs    error
	}{
		{"deleted", true, nil, false, nil},
		{"not found", true, sql.ErrNoRows, true, ErrNotFound},
		{"server error", true, errors.New("headscale didn't stop"), true, nil},
		{"server not running", false, nil, true, ErrNotRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Unix socket paths are limited to ~100 bytes.
			dataDir, err := os.MkdirTemp("", "ctl")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dataDir) })
			var deleted string
			if tt.serve {
				ln, err := net.Listen("unix", SocketPath(dataDir))
				if err != nil {
					t.Fatal(err)
				}
				deleteOrg := func(_ context.Context, sub string) error {
					if tt.deleteErr != nil {
						return tt.deleteErr
					}
					deleted = sub
					return nil
				}
				srv := &http.Server{Handler: Handler(deleteOrg, slog.New(slog.DiscardHandler))}
				go func() { _ = srv.Serve(ln) }()
				t.Cleanup(func() { _ = srv.Close() })
			}

			err = NewClient(dataDir).DeleteOrganization(t.Context(), "acme")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("err = %v, want %v", err, tt.wantIs)
			}
			if wantDeleted := !tt.wantErr; (deleted == "acme") != wantDeleted {
				t.Errorf("deleted = %q, want deleted %v", deleted, wantDeleted)
			}
		})
	}
}
