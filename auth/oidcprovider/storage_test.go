package oidcprovider

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// TestCreateAuthRequestLimits verifies pending auth requests can't grow
// memory without bound: oversized ones are refused, and a full store only
// takes new ones once expired ones make room.
func TestCreateAuthRequestLimits(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		pending int
		age     time.Duration
		wantErr bool
	}{
		{name: "small", state: "xyz"},
		{name: "oversized state", state: strings.Repeat("x", maxAuthRequestBytes+1), wantErr: true},
		{name: "full", state: "xyz", pending: maxAuthRequests, wantErr: true},
		{name: "full of expired", state: "xyz", pending: maxAuthRequests, age: 2 * authRequestLifetime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage, _ := testStorage(t)
			created := time.Now().Add(-tt.age)
			for i := range tt.pending {
				storage.authRequests[strconv.Itoa(i)] = &authRequest{CreationDate: created}
			}
			_, err := storage.CreateAuthRequest(t.Context(), &oidc.AuthRequest{
				ClientID:     "client",
				RedirectURI:  "https://acme.example.com/oidc/callback",
				ResponseType: oidc.ResponseTypeCode,
				Scopes:       []string{oidc.ScopeOpenID},
				State:        tt.state,
			}, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("CreateAuthRequest: err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(storage.authRequests) > maxAuthRequests {
				t.Errorf("%d pending auth requests, want at most %d", len(storage.authRequests), maxAuthRequests)
			}
		})
	}
}
