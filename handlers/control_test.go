package handlers

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
)

func TestDeleteOrganization(t *testing.T) {
	tests := []struct {
		name      string
		subdomain string
		wantErr   error
		wantOrg   bool
	}{
		{"deletes the organization and its files", "acme", nil, false},
		{"unknown organization", "nope", sql.ErrNoRows, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := shortTempDir(t)
			h, queries := newTestHandlersWithDataDir(t, "", dataDir)
			dbtest.CreateOrg(t, queries.Queries, "acme")
			if err := os.MkdirAll(filepath.Join(dataDir, "acme"), 0o755); err != nil {
				t.Fatal(err)
			}

			err := h.DeleteOrganization(t.Context(), tt.subdomain)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("DeleteOrganization err = %v, want %v", err, tt.wantErr)
			}
			_, dbErr := queries.GetOrganizationBySubdomain(t.Context(), "acme")
			_, dirErr := os.Stat(filepath.Join(dataDir, "acme"))
			if (dbErr == nil) != tt.wantOrg || (dirErr == nil) != tt.wantOrg {
				t.Errorf("row err %v, dir err %v; want organization present = %v", dbErr, dirErr, tt.wantOrg)
			}
		})
	}
}
