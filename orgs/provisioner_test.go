package orgs

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/ownscalenet/headscale/hscontrol"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// newTestService builds a Service backed by a temporary sqlite DB with
// a noop createFn so tests don't attempt to spin up a real headscale
// instance.
func newTestService(t *testing.T) *Service {
	t.Helper()
	svc := NewService(newTestQueries(t), "example.com", "https://auth.example.com", config.DerpConfig{}, discardLogger(), t.TempDir())
	svc.createFn = noopCreateFn
	return svc
}

// noopCreateFn is a createFn that immediately returns nil (no headscale
// instance started).
func noopCreateFn(_ context.Context, _, _, _ string, _ int, _ OIDCClientCredentials, _ config.DerpConfig) (*hscontrol.Headscale, error) {
	return nil, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCreateReturnsActiveOrganization(t *testing.T) {
	svc := newTestService(t)

	org, err := svc.Create(t.Context(), "myteam")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if org.Status != StatusActive {
		t.Errorf("org.Status = %q, want %q", org.Status, StatusActive)
	}
	if org.Subdomain != "myteam" {
		t.Errorf("org.Subdomain = %q, want %q", org.Subdomain, "myteam")
	}
}

func TestCreateRejectsInvalidSubdomain(t *testing.T) {
	svc := newTestService(t)

	if _, err := svc.Create(t.Context(), "INVALID!"); err == nil {
		t.Fatal("Create with invalid subdomain: want error, got nil")
	}
}

func TestCreateSurfacesHeadscaleFailure(t *testing.T) {
	svc := newTestService(t)
	svc.createFn = func(_ context.Context, _, _, _ string, _ int, _ OIDCClientCredentials, _ config.DerpConfig) (*hscontrol.Headscale, error) {
		return nil, errors.New("headscale failed (injected)")
	}

	if _, err := svc.Create(t.Context(), "acme"); err == nil {
		t.Fatal("Create: want error when createFn fails, got nil")
	}
	if _, err := svc.queries.GetOrganizationBySubdomain(t.Context(), "acme"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("organization row after failed Create: err = %v, want sql.ErrNoRows", err)
	}
}

func TestNextSubdomain(t *testing.T) {
	tests := []struct {
		name  string
		taken []string
		want  string // "" means a random subdomain
	}{
		{"empty database", nil, "aaaa"},
		{"skips taken fixed subdomains", []string{"aaaa", "bbbb"}, "cccc"},
		{"falls back to random", fixedSubdomains, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			for _, sub := range tt.taken {
				if _, err := svc.Create(t.Context(), sub); err != nil {
					t.Fatalf("Create(%q): %v", sub, err)
				}
			}
			got, err := svc.NextSubdomain(t.Context())
			if err != nil {
				t.Fatalf("NextSubdomain: %v", err)
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("NextSubdomain = %q, want %q", got, tt.want)
			}
			if tt.want == "" {
				if slices.Contains(fixedSubdomains, got) {
					t.Errorf("NextSubdomain = %q, want a random subdomain", got)
				}
				if err := ValidateSubdomain(got); err != nil {
					t.Errorf("random subdomain %q is invalid: %v", got, err)
				}
			}
		})
	}
}

// TestStartRecoversOrganizations checks that Start restarts the headscale
// instance of an organization left over from a previous process.
func TestStartRecoversOrganizations(t *testing.T) {
	queries := newTestQueries(t)
	seed := NewService(queries, "example.com", "https://auth.example.com", config.DerpConfig{}, discardLogger(), t.TempDir())
	seed.createFn = noopCreateFn
	if _, err := seed.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A second Service simulates a process restart.
	svc := NewService(queries, "example.com", "https://auth.example.com", config.DerpConfig{}, discardLogger(), t.TempDir())
	svc.createFn = noopCreateFn

	if err := svc.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := svc.ports.Load("acme"); !ok {
		t.Error("port not registered for recovered org \"acme\"")
	}
}

// TestStartRecoveryWithNoOrgsIsNoop verifies Start doesn't error on an
// empty DB.
func TestStartRecoveryWithNoOrgsIsNoop(t *testing.T) {
	svc := newTestService(t)

	if err := svc.Start(t.Context()); err != nil {
		t.Fatalf("Start on empty DB: %v", err)
	}
}

// TestCreateSeedsOrgConfig verifies that Create writes the headscale
// config YAML (with issuer) to orgconfigs.
func TestCreateSeedsOrgConfig(t *testing.T) {
	svc := newTestService(t)

	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cfgRow, err := svc.queries.GetOrgConfig(t.Context(), sqlcgen.GetOrgConfigParams{
		OrganizationID: org.ID,
		Path:           ConfigYAMLPath,
	})
	if err != nil {
		t.Fatalf("GetOrgConfig: %v", err)
	}
	if cfgRow.Content == "" {
		t.Error("orgconfigs row is empty after Create")
	}
}
