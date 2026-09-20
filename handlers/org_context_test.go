package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownscalenet/ownscale/headscaleclient"
)

// TestRequireHeadscaleClientShortCircuits covers RequireHeadscaleClient's
// guard: a mint-key failure short-circuits with 409+notConfigured without
// calling next.
func TestRequireHeadscaleClientShortCircuits(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _, _ := activeOrgUser(t, h, queries)
	h.hs = headscaleclient.NewRegistry(&fakeClientMinter{err: errors.New("mint failed")})

	nextCalled := false
	next := func(http.ResponseWriter, *http.Request) { nextCalled = true }

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user)
	rec := httptest.NewRecorder()
	h.RequireHeadscaleClient(next)(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
	if nextCalled {
		t.Error("next was called, want short-circuit")
	}
}

func TestRequireHeadscaleClientCallsNextWithClient(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _, _ := activeOrgUser(t, h, queries)

	var gotClient bool
	next := func(w http.ResponseWriter, r *http.Request) {
		_, _, gotClient = headscaleClientFromContext(r)
		w.WriteHeader(http.StatusOK)
	}

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user)
	rec := httptest.NewRecorder()
	h.RequireHeadscaleClient(next)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !gotClient {
		t.Error("expected a client attached to the request context")
	}
}
