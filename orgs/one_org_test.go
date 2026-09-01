package orgs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"reflect"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
	"github.com/ownscalenet/ownscale/headscaleclient"
)

// newAuthRestrictionsTestService builds a Service backed by a dockerfake
// API and a real temp dataDir (so SetAuthRestrictions has an actual
// config.yaml orgconfigs row/on-disk file to patch, seeded by
// provision/CreateOrganization), recording every container restarted so
// tests can assert on it without a real Docker daemon.
func newAuthRestrictionsTestService(t *testing.T) (svc *Service, restarted *[]string) {
	t.Helper()
	queries := newTestQueries(t)
	var restartedContainers []string
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, cfg *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		ContainerRestartFn: func(ctx context.Context, containerID string, options container.StopOptions) error {
			restartedContainers = append(restartedContainers, containerID)
			return nil
		},
	}
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	ctl.MintClientFn = func(ctx context.Context, subdomain string) (*headscaleclient.Client, error) {
		return newFakePolicyClient(t), nil
	}
	t.Cleanup(func() { _ = ctl.Close() })

	svc = NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})
	return svc, &restartedContainers
}

// TestGetAuthRestrictionsDefaultsToEmptyWhenMissing verifies
// GetAuthRestrictions doesn't error for an organization with no
// orgconfigs row (e.g. a brand new one, or one provisioned before this
// feature existed), returning defaultAuthRestrictions instead.
func TestGetAuthRestrictionsDefaultsToEmptyWhenMissing(t *testing.T) {
	svc, _ := newAuthRestrictionsTestService(t)

	content, err := svc.ForOrg(sqlcgen.Organization{ID: "nonexistent-org-id"}).GetAuthRestrictions(t.Context())
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != defaultAuthRestrictions {
		t.Fatalf("content = %q, want default", content)
	}
}

// TestSetAuthRestrictionsRejectsInvalidHuJSON verifies SetAuthRestrictions
// validates content before persisting or touching Docker.
func TestSetAuthRestrictionsRejectsInvalidHuJSON(t *testing.T) {
	svc, restarted := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	*restarted = nil

	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), "not valid hujson {{{"); err == nil {
		t.Fatal("SetAuthRestrictions: want error for invalid HuJSON, got nil")
	}

	if len(*restarted) != 0 {
		t.Fatalf("headscale restarted for invalid content: %v", *restarted)
	}
}

func TestSetAuthRestrictionsRollsBackOnDockerFailure(t *testing.T) {
	queries := newTestQueries(t)
	var failRestart bool
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, cfg *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		ContainerRestartFn: func(ctx context.Context, containerID string, options container.StopOptions) error {
			if failRestart {
				return errors.New("restart failed")
			}
			return nil
		},
	}
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	ctl.MintClientFn = func(ctx context.Context, subdomain string) (*headscaleclient.Client, error) {
		return newFakePolicyClient(t), nil
	}
	t.Cleanup(func() { ctl.Close() })

	svc := NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	failRestart = true
	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), `{"domains":["example.com"]}`); err == nil {
		t.Fatal("SetAuthRestrictions: want error when restart fails, got nil")
	}

	content, err := svc.ForOrg(org).GetAuthRestrictions(t.Context())
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != defaultAuthRestrictions {
		t.Fatalf("GetAuthRestrictions = %q, want the pre-failure default (rolled back)", content)
	}
}

// TestSetAuthRestrictionsPersistsPatchesAndRestarts verifies
// SetAuthRestrictions stores the new allowlist, patches it into the
// running headscale container's config.yaml, and restarts headscale so it
// reloads it.
func TestSetAuthRestrictionsPersistsPatchesAndRestarts(t *testing.T) {
	svc, restarted := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	*restarted = nil

	newContent := `{"domains":["example.com"],"groups":["group:eng"],"users":["alice@example.com"]}`
	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), newContent); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}

	content, err := svc.ForOrg(org).GetAuthRestrictions(t.Context())
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != newContent {
		t.Fatalf("GetAuthRestrictions = %q, want %q", content, newContent)
	}
	if len(*restarted) != 1 || (*restarted)[0] != dockerctl.HSContainerName("acme") {
		t.Fatalf("restarted = %v, want [%q]", *restarted, dockerctl.HSContainerName("acme"))
	}

	cfg, err := svc.ForOrg(org).getHeadscaleConfig(t.Context())
	if err != nil {
		t.Fatalf("getHeadscaleConfig: %v", err)
	}
	if got := cfg.OIDC.AllowedDomains; len(got) != 1 || got[0] != "example.com" {
		t.Errorf("config.yaml oidc.allowed_domains = %v, want [example.com]", got)
	}
	if got := cfg.OIDC.AllowedGroups; len(got) != 1 || got[0] != "group:eng" {
		t.Errorf("config.yaml oidc.allowed_groups = %v, want [group:eng]", got)
	}
	if got := cfg.OIDC.AllowedUsers; len(got) != 1 || got[0] != "alice@example.com" {
		t.Errorf("config.yaml oidc.allowed_users = %v, want [alice@example.com]", got)
	}
}

// TestSetAuthRestrictionsCanClearAllowlist verifies saving an empty
// allowlist after a previous non-empty save clears the stored one.
func TestSetAuthRestrictionsCanClearAllowlist(t *testing.T) {
	svc, _ := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), `{"domains":["example.com"]}`); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}
	emptyContent := `{"domains":[],"groups":[],"users":[]}`
	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), emptyContent); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}

	content, err := svc.ForOrg(org).GetAuthRestrictions(t.Context())
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != emptyContent {
		t.Fatalf("GetAuthRestrictions = %q, want %q", content, emptyContent)
	}
}

func TestGetDNSSettingsReflectsProvisionedDefaults(t *testing.T) {
	svc, _ := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	dns, err := svc.ForOrg(org).GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}
	if !dns.MagicDNS {
		t.Errorf("MagicDNS = false, want true")
	}
	if dns.BaseDomain != "acme.ownscale" {
		t.Errorf("BaseDomain = %q, want %q", dns.BaseDomain, "acme.ownscale")
	}
}

func TestSetDNSSettingsPersistsAndRestarts(t *testing.T) {
	svc, restarted := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	*restarted = nil

	want := dockerctl.DNSConfig{
		BaseDomain:    "acme.headscale.internal",
		MagicDNS:      true,
		Nameservers:   dockerctl.Nameservers{Global: []string{"1.1.1.1", "8.8.8.8"}},
		SearchDomains: []string{"internal.acme.example.com"},
		ExtraRecords:  []dockerctl.DNSRecord{{Name: "server.acme.headscale.internal", Type: "A", Value: "10.0.0.1"}},
	}
	if err := svc.ForOrg(org).SetDNSSettings(t.Context(), want); err != nil {
		t.Fatalf("SetDNSSettings: %v", err)
	}

	got, err := svc.ForOrg(org).GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}
	if len(got.Nameservers.Global) != 2 || got.Nameservers.Global[1] != "8.8.8.8" {
		t.Errorf("Nameservers = %v, want %v", got.Nameservers.Global, want.Nameservers.Global)
	}
	if len(got.ExtraRecords) != 1 || got.ExtraRecords[0] != want.ExtraRecords[0] {
		t.Errorf("ExtraRecords = %v, want %v", got.ExtraRecords, want.ExtraRecords)
	}
	if len(*restarted) != 1 {
		t.Fatalf("restarted %d times, want 1", len(*restarted))
	}
}

func TestSetDNSSettingsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		dns  dockerctl.DNSConfig
	}{
		{"empty base domain", dockerctl.DNSConfig{BaseDomain: ""}},
		{"empty record name", dockerctl.DNSConfig{BaseDomain: "acme.headscale.internal", ExtraRecords: []dockerctl.DNSRecord{{Name: "", Type: "A", Value: "1.2.3.4"}}}},
		{"empty record value", dockerctl.DNSConfig{BaseDomain: "acme.headscale.internal", ExtraRecords: []dockerctl.DNSRecord{{Name: "a.example.com", Type: "A", Value: ""}}}},
		{"duplicate record", dockerctl.DNSConfig{BaseDomain: "acme.headscale.internal", ExtraRecords: []dockerctl.DNSRecord{
			{Name: "a.example.com", Type: "A", Value: "1.2.3.4"},
			{Name: "a.example.com", Type: "A", Value: "5.6.7.8"},
		}}},
	}

	svc, restarted := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*restarted = nil
			err := svc.ForOrg(org).SetDNSSettings(t.Context(), tt.dns)
			if err == nil {
				t.Fatal("SetDNSSettings: want error, got nil")
			}
			if _, ok := errors.AsType[*dockerctl.InvalidConfigError](err); !ok {
				t.Fatalf("SetDNSSettings error = %v, want *dockerctl.InvalidConfigError", err)
			}
			if len(*restarted) != 0 {
				t.Fatalf("headscale restarted for invalid input: %v", *restarted)
			}
		})
	}
}

func TestSetDNSSettingsRollsBackOnConfigTestFailure(t *testing.T) {
	queries := newTestQueries(t)
	var restartedContainers []string
	var failConfigTest bool
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, cfg *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		ContainerRestartFn: func(ctx context.Context, containerID string, options container.StopOptions) error {
			restartedContainers = append(restartedContainers, containerID)
			return nil
		},
		ContainerExecInspectFn: func(ctx context.Context, execID string) (container.ExecInspect, error) {
			if failConfigTest {
				return container.ExecInspect{ExitCode: 1}, nil
			}
			return container.ExecInspect{ExitCode: 0}, nil
		},
		ContainerExecAttachFn: func(ctx context.Context, execID string, options container.ExecAttachOptions) (types.HijackedResponse, error) {
			clientConn, serverConn := net.Pipe()
			go func() {
				if failConfigTest {
					w := stdcopy.NewStdWriter(serverConn, stdcopy.Stderr)
					_, _ = w.Write([]byte("dns.base_domain must be set when using MagicDNS"))
				} else {
					w := stdcopy.NewStdWriter(serverConn, stdcopy.Stdout)
					_, _ = w.Write([]byte("fake-headscale-api-key\n"))
				}
				_ = serverConn.Close()
			}()
			return types.NewHijackedResponse(clientConn, ""), nil
		},
	}
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	ctl.MintClientFn = func(ctx context.Context, subdomain string) (*headscaleclient.Client, error) {
		return newFakePolicyClient(t), nil
	}
	t.Cleanup(func() { _ = ctl.Close() })

	svc := NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	original, err := svc.ForOrg(org).GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}

	failConfigTest = true
	bad := dockerctl.DNSConfig{BaseDomain: "acme.headscale.internal", MagicDNS: true}
	err = svc.ForOrg(org).SetDNSSettings(t.Context(), bad)
	if err == nil {
		t.Fatal("SetDNSSettings: want error when configtest fails, got nil")
	}
	if _, ok := errors.AsType[*dockerctl.InvalidConfigError](err); !ok {
		t.Fatalf("SetDNSSettings error = %v, want *dockerctl.InvalidConfigError", err)
	}
	if len(restartedContainers) != 0 {
		t.Fatalf("headscale restarted despite configtest failure: %v", restartedContainers)
	}

	got, err := svc.ForOrg(org).GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("GetDNSSettings after rollback = %+v, want unchanged %+v", got, original)
	}
}
