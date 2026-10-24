package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/orgs"
)

// TestRequireHeadscaleClient verifies that next only runs with the
// organization's client, and that headscale that can't run answers 503.
func TestRequireHeadscaleClient(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"running", nil, http.StatusOK},
		{"not running", orgs.ErrNotRunning, http.StatusServiceUnavailable},
		{"database missing", orgs.ErrDatabaseMissing, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, queries := newTestHandlers(t)
			user, client, _ := activeOrgUser(t, h, queries)
			if tt.err != nil {
				h.hs = fakeHeadscaleClients{err: tt.err}
			}

			var gotClient *hscl.Client
			next := func(w http.ResponseWriter, r *http.Request) {
				gotClient, _ = headscaleClientFromContext(r)
				w.WriteHeader(http.StatusOK)
			}
			req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user)
			rec := httptest.NewRecorder()
			h.RequireHeadscaleClient(next)(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.err == nil && gotClient != client {
				t.Errorf("next got client %p, want the organization's %p", gotClient, client)
			}
			if tt.err != nil && gotClient != nil {
				t.Error("next ran without a running headscale")
			}
		})
	}
}
