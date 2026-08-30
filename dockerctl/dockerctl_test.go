package dockerctl

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
	"github.com/ownscalenet/ownscale/localca"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBuildHSConfigIncludesOIDC(t *testing.T) {
	cfg, err := BuildHeadscaleConfig("acme", "acme.example.com", OIDCClientCredentials{
		Issuer:       "https://auth.example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, config.DerpConfig{})
	if err != nil {
		t.Fatalf("BuildHeadscaleConfig: %v", err)
	}
	for _, want := range []string{
		`issuer: "https://auth.example.com"`,
		`client_id: "client-id"`,
		`client_secret: "client-secret"`,
		`method: S256`,
		"https://controlplane.tailscale.com/derpmap/default",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "paths:") {
		t.Fatalf("config should not reference a custom derp.paths when no DerpConfig is set:\n%s", cfg)
	}
}

func TestBuildHSConfigWithCustomDerp(t *testing.T) {
	cfg, err := BuildHeadscaleConfig("acme", "acme.example.com", OIDCClientCredentials{}, config.DerpConfig{
		Hostname:   "derp.example.com",
		STUNPort:   3478,
		RegionID:   900,
		RegionCode: "custom",
		RegionName: "Self-hosted DERP",
	})
	if err != nil {
		t.Fatalf("BuildHeadscaleConfig: %v", err)
	}
	for _, want := range []string{
		"paths:",
		derpConfigPath,
		"urls: []",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "controlplane.tailscale.com") {
		t.Fatalf("config should not reference Tailscale's public derp map when a custom DerpConfig is set:\n%s", cfg)
	}
}

func TestBuildDerpConfig(t *testing.T) {
	cfg, err := buildDerpConfig(config.DerpConfig{
		Hostname:   "derp.example.com",
		IPv4:       "198.51.100.1",
		IPv6:       "2001:db8::1",
		STUNPort:   3478,
		RegionID:   900,
		RegionCode: "custom",
		RegionName: "Self-hosted DERP",
	})
	if err != nil {
		t.Fatalf("buildDerpConfig: %v", err)
	}
	for _, want := range []string{
		"regionid: 900",
		"regioncode: custom",
		"regionname: Self-hosted DERP",
		"hostname: derp.example.com",
		"ipv4: 198.51.100.1",
		"ipv6: 2001:db8::1",
		"stunport: 3478",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("derp config missing %q:\n%s", want, cfg)
		}
	}
}

func TestBuildHPConfigIncludesOIDCAndAPIKey(t *testing.T) {
	cfg, err := buildHPConfig("acme", "acme.example.com", "cookie-secret", "headscale-api-key", OIDCClientCredentials{
		Issuer:       "https://auth.example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	})
	if err != nil {
		t.Fatalf("buildHPConfig: %v", err)
	}
	for _, want := range []string{
		`api_key: "headscale-api-key"`,
		`issuer: "https://auth.example.com"`,
		`token_endpoint_auth_method: "client_secret_basic"`,
		`client_id: "client-id"`,
		`client_secret: "client-secret"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}
}

// TestBuildHPConfigEnablesDockerIntegration verifies that the rendered
// Headplane config enables the Docker integration (Network Management)
// and points it at this org's own headscale container by name.
func TestBuildHPConfigEnablesDockerIntegration(t *testing.T) {
	cfg, err := buildHPConfig("acme", "acme.example.com", "cookie-secret", "headscale-api-key", OIDCClientCredentials{})
	if err != nil {
		t.Fatalf("buildHPConfig: %v", err)
	}
	for _, want := range []string{
		"docker:\n    enabled: true",
		`container_name: "hs-acme"`,
		`socket: "unix:///var/run/docker.sock"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}
}

func TestCreateOrganizationBootstrapsHeadscaleAPIKey(t *testing.T) {
	copied := make(map[string]map[string]string)
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		CopyToContainerFn: func(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error {
			files, err := tarContents(content)
			if err != nil {
				return err
			}
			if copied[containerID] == nil {
				copied[containerID] = make(map[string]string)
			}
			maps.Copy(copied[containerID], files)
			return nil
		},
		ContainerExecCreateFn: func(ctx context.Context, containerID string, options container.ExecOptions) (container.ExecCreateResponse, error) {
			return container.ExecCreateResponse{ID: "exec-1"}, nil
		},
		ContainerExecAttachFn: func(ctx context.Context, execID string, options container.ExecAttachOptions) (types.HijackedResponse, error) {
			clientConn, serverConn := net.Pipe()
			go func() {
				w := stdcopy.NewStdWriter(serverConn, stdcopy.Stdout)
				_, _ = w.Write([]byte("headscale-api-key\n"))
				_ = serverConn.Close()
			}()
			return types.NewHijackedResponse(clientConn, ""), nil
		},
		ContainerExecInspectFn: func(ctx context.Context, execID string) (container.ExecInspect, error) {
			return container.ExecInspect{ExitCode: 0}, nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	_, err = ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{
		Issuer:       "https://auth.example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, config.DerpConfig{}, DefaultACLPolicy)
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	hpCfg := copied[HPContainerName("acme")][strings.TrimPrefix(headplaneConfigPath, "/")]
	if !strings.Contains(hpCfg, `api_key: "headscale-api-key"`) || !strings.Contains(hpCfg, `client_id: "client-id"`) {
		t.Fatalf("unexpected headplane config:\n%s", hpCfg)
	}
	hsCfg, err := os.ReadFile(filepath.Join(ctl.headscaleConfigHostDir("acme"), "config.yaml"))
	if err != nil {
		t.Fatalf("read headscale config file: %v", err)
	}
	if !strings.Contains(string(hsCfg), `issuer: "https://auth.example.com"`) {
		t.Fatalf("unexpected headscale config:\n%s", hsCfg)
	}
}

func tarContents(r io.Reader) (map[string]string, error) {
	tr := tar.NewReader(r)
	files := make(map[string]string)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, tr); err != nil {
			return nil, err
		}
		files[hdr.Name] = buf.String()
	}
}

// TestEnsureCaddyNetworkAlias covers every combination of "is caddy
// running at all" and "is caddy already connected to the network, with
// the right alias" ensureCaddyNetworkAlias needs to handle.
func TestEnsureCaddyNetworkAlias(t *testing.T) {
	cases := []struct {
		name string
		// containerFound selects whether ContainerList finds a running
		// caddy container at all.
		containerFound bool
		// initialNetworks are the networks (and their current aliases)
		// caddy's ContainerInspect reports it's already connected to.
		initialNetworks map[string]*network.EndpointSettings
		wantDisconnect  bool
		wantConnect     bool
		// wantAliases, when wantConnect, are the aliases the (re)connect
		// call must include.
		wantAliases []string
	}{
		{
			name:           "no caddy found",
			containerFound: false,
		},
		{
			name:            "connects when not attached",
			containerFound:  true,
			initialNetworks: map[string]*network.EndpointSettings{},
			wantConnect:     true,
			wantAliases:     []string{"example.com"},
		},
		{
			name:           "reconnects when alias missing",
			containerFound: true,
			initialNetworks: map[string]*network.EndpointSettings{
				ownscaleNetwork: {Aliases: []string{"caddy"}},
			},
			wantDisconnect: true,
			wantConnect:    true,
			wantAliases:    []string{"example.com", "caddy"},
		},
		{
			name:           "no-op when already aliased",
			containerFound: true,
			initialNetworks: map[string]*network.EndpointSettings{
				ownscaleNetwork: {Aliases: []string{"example.com"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var disconnected, connected bool
			var gotAliases []string
			api := &dockerfake.API{
				ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
					if !tc.containerFound {
						return nil, nil
					}
					return []container.Summary{{ID: "caddy-1"}}, nil
				},
				ContainerInspectFn: func(ctx context.Context, containerID string) (container.InspectResponse, error) {
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "running"}},
						NetworkSettings:   &container.NetworkSettings{Networks: tc.initialNetworks},
					}, nil
				},
				NetworkConnectFn: func(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error {
					if !tc.wantConnect {
						t.Error("NetworkConnect: want no connect")
					}
					connected = true
					if networkID != ownscaleNetwork || containerID != "caddy-1" {
						t.Errorf("NetworkConnect(%q, %q), want (%q, %q)", networkID, containerID, ownscaleNetwork, "caddy-1")
					}
					gotAliases = config.Aliases
					return nil
				},
				NetworkDisconnectFn: func(ctx context.Context, networkID, containerID string, force bool) error {
					if !tc.wantDisconnect {
						t.Error("NetworkDisconnect: want no disconnect")
					}
					disconnected = true
					return nil
				},
			}
			ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
			if err != nil {
				t.Fatalf("NewWithClient: %v", err)
			}
			t.Cleanup(func() { _ = ctl.Close() })

			if err := ctl.ensureCaddyNetworkAlias(t.Context(), ownscaleNetwork, "example.com"); err != nil {
				t.Fatalf("ensureCaddyNetworkAlias: %v", err)
			}
			if disconnected != tc.wantDisconnect {
				t.Errorf("disconnected = %v, want %v", disconnected, tc.wantDisconnect)
			}
			if connected != tc.wantConnect {
				t.Errorf("connected = %v, want %v", connected, tc.wantConnect)
			}
			for _, want := range tc.wantAliases {
				if !slices.Contains(gotAliases, want) {
					t.Errorf("aliases = %v, want it to contain %q", gotAliases, want)
				}
			}
		})
	}
}

func TestCreateOrganizationMountsLocalCADirAndSetsEnvWhenConfigured(t *testing.T) {
	var hsMounts, hpMounts []mount.Mount
	var hsEnv, hpEnv []string
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			switch containerName {
			case HSContainerName("acme"):
				hsMounts = hostConfig.Mounts
				hsEnv = config.Env
			case HPContainerName("acme"):
				hpMounts = hostConfig.Mounts
				hpEnv = config.Env
			}
			return container.CreateResponse{ID: containerName}, nil
		},
	}
	localCADir := t.TempDir()
	ctl, err := NewWithClient(api, t.TempDir(), localCADir, discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{
		Issuer: "https://example.com", ClientID: "id", ClientSecret: "secret",
	}, config.DerpConfig{}, DefaultACLPolicy); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	for _, mounts := range [][]mount.Mount{hsMounts, hpMounts} {
		if !slices.ContainsFunc(mounts, func(m mount.Mount) bool {
			return m.Type == mount.TypeBind && m.Source == localCADir && m.Target == LocalCAMountTarget && m.ReadOnly
		}) {
			t.Fatalf("mounts = %+v, want a read-only bind mount of %q at %q", mounts, localCADir, LocalCAMountTarget)
		}
	}

	wantHSEnv := "SSL_CERT_DIR=/etc/ssl/certs:/etc/pki/tls/certs:" + LocalCAMountTarget
	if !slices.Contains(hsEnv, wantHSEnv) {
		t.Fatalf("headscale env = %v, want it to contain %q", hsEnv, wantHSEnv)
	}

	wantHPEnv := "NODE_EXTRA_CA_CERTS=" + LocalCAMountTarget + "/" + localca.RootCertFileName
	if !slices.Contains(hpEnv, wantHPEnv) {
		t.Fatalf("headplane env = %v, want it to contain %q", hpEnv, wantHPEnv)
	}
}

func TestCreateOrganizationSkipsLocalCAMountWhenNotConfigured(t *testing.T) {
	var hsMounts, hpMounts []mount.Mount
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			switch containerName {
			case HSContainerName("acme"):
				hsMounts = hostConfig.Mounts
			case HPContainerName("acme"):
				hpMounts = hostConfig.Mounts
			}
			return container.CreateResponse{ID: containerName}, nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{
		Issuer: "https://example.com", ClientID: "id", ClientSecret: "secret",
	}, config.DerpConfig{}, DefaultACLPolicy); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	for _, mounts := range [][]mount.Mount{hsMounts, hpMounts} {
		if slices.ContainsFunc(mounts, func(m mount.Mount) bool { return m.Target == LocalCAMountTarget }) {
			t.Fatalf("mounts = %+v, want no local CA mount when LocalCADir is unset", mounts)
		}
	}
}

// TestCreateOrganizationSharesHeadscaleConfigWithHeadplane verifies that
// the headscale config file is bind-mounted (read-only) into the headscale
// container and the exact same host file is bind-mounted (read-write) into
// the headplane container, both at ConfigYAMLPath -- this is what
// lets Headplane's Network Management feature edit live headscale settings
// and have headscale pick them up.
func TestCreateOrganizationSharesHeadscaleConfigWithHeadplane(t *testing.T) {
	var hsMounts, hpMounts []mount.Mount
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			switch containerName {
			case HSContainerName("acme"):
				hsMounts = hostConfig.Mounts
			case HPContainerName("acme"):
				hpMounts = hostConfig.Mounts
			}
			return container.CreateResponse{ID: containerName}, nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{
		Issuer: "https://example.com", ClientID: "id", ClientSecret: "secret",
	}, config.DerpConfig{}, DefaultACLPolicy); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	wantSource := filepath.Join(ctl.headscaleConfigHostDir("acme"), "config.yaml")

	hsIdx := slices.IndexFunc(hsMounts, func(m mount.Mount) bool { return m.Target == ConfigYAMLPath })
	if hsIdx < 0 {
		t.Fatalf("headscale mounts = %+v, want a bind mount at %q", hsMounts, ConfigYAMLPath)
	}
	if hsMounts[hsIdx].Source != wantSource || !hsMounts[hsIdx].ReadOnly {
		t.Fatalf("headscale config mount = %+v, want read-only bind mount of %q", hsMounts[hsIdx], wantSource)
	}

	hpIdx := slices.IndexFunc(hpMounts, func(m mount.Mount) bool { return m.Target == ConfigYAMLPath })
	if hpIdx < 0 {
		t.Fatalf("headplane mounts = %+v, want a bind mount at %q", hpMounts, ConfigYAMLPath)
	}
	if hpMounts[hpIdx].Source != wantSource || hpMounts[hpIdx].ReadOnly {
		t.Fatalf("headplane config mount = %+v, want read-write bind mount of %q", hpMounts[hpIdx], wantSource)
	}

	content, err := os.ReadFile(wantSource)
	if err != nil {
		t.Fatalf("read shared config file: %v", err)
	}
	if !strings.Contains(string(content), `issuer: "https://example.com"`) {
		t.Fatalf("shared config file content = %s, want it to contain the rendered headscale config", content)
	}
}

// TestCreateOrganizationMountsDockerSocketIntoHeadplane verifies the
// headplane container gets a read-only bind mount of the host Docker
// socket, which it needs to restart the headscale container after Network
// Management edits its config file.
func TestCreateOrganizationMountsDockerSocketIntoHeadplane(t *testing.T) {
	var hsMounts, hpMounts []mount.Mount
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			switch containerName {
			case HSContainerName("acme"):
				hsMounts = hostConfig.Mounts
			case HPContainerName("acme"):
				hpMounts = hostConfig.Mounts
			}
			return container.CreateResponse{ID: containerName}, nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{}, config.DerpConfig{}, DefaultACLPolicy); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	if !slices.ContainsFunc(hpMounts, func(m mount.Mount) bool {
		return m.Type == mount.TypeBind && m.Source == "/var/run/docker.sock" && m.Target == "/var/run/docker.sock" && m.ReadOnly
	}) {
		t.Fatalf("headplane mounts = %+v, want a read-only bind mount of the Docker socket", hpMounts)
	}
	if slices.ContainsFunc(hsMounts, func(m mount.Mount) bool { return m.Source == "/var/run/docker.sock" }) {
		t.Fatalf("headscale mounts = %+v, want no Docker socket mount", hsMounts)
	}
}

// TestCreateOrganizationUsesPerOrgNetwork verifies that both the headscale
// and headplane containers are created on the organization's own isolated
// network (orgNetworkName), not the shared ownscaleNetwork.
func TestCreateOrganizationUsesPerOrgNetwork(t *testing.T) {
	var hsNetworks, hpNetworks map[string]*network.EndpointSettings
	var caddyConnectedNetwork string
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			switch containerName {
			case HSContainerName("acme"):
				hsNetworks = networkingConfig.EndpointsConfig
			case HPContainerName("acme"):
				hpNetworks = networkingConfig.EndpointsConfig
			}
			return container.CreateResponse{ID: containerName}, nil
		},
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{{ID: "caddy-1"}}, nil
		},
		ContainerInspectFn: func(ctx context.Context, containerID string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "running"}},
				NetworkSettings:   &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{}},
			}, nil
		},
		NetworkConnectFn: func(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error {
			caddyConnectedNetwork = networkID
			return nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{}, config.DerpConfig{}, DefaultACLPolicy); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	wantNetwork := OrgNetworkName("acme")
	if _, ok := hsNetworks[wantNetwork]; !ok {
		t.Fatalf("headscale networks = %+v, want it to include %q", hsNetworks, wantNetwork)
	}
	if _, ok := hsNetworks[ownscaleNetwork]; ok {
		t.Fatalf("headscale networks = %+v, want it NOT to include the shared %q", hsNetworks, ownscaleNetwork)
	}
	if _, ok := hpNetworks[wantNetwork]; !ok {
		t.Fatalf("headplane networks = %+v, want it to include %q", hpNetworks, wantNetwork)
	}
	if _, ok := hpNetworks[ownscaleNetwork]; ok {
		t.Fatalf("headplane networks = %+v, want it NOT to include the shared %q", hpNetworks, ownscaleNetwork)
	}
	if caddyConnectedNetwork != wantNetwork {
		t.Fatalf("caddy connected to network %q, want %q", caddyConnectedNetwork, wantNetwork)
	}
}

// TestEnsureOrgNetwork covers both branches: creating the network when
// missing, and leaving it alone (never calling NetworkCreate) when it
// already exists.
func TestEnsureOrgNetwork(t *testing.T) {
	cases := []struct {
		name          string
		networkExists bool
	}{
		{name: "creates if missing", networkExists: false},
		{name: "no-op when already exists", networkExists: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var inspected, created string
			api := &dockerfake.API{
				NetworkInspectFn: func(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
					inspected = networkID
					if tc.networkExists {
						return network.Inspect{}, nil
					}
					return network.Inspect{}, cerrdefs.ErrNotFound
				},
				NetworkCreateFn: func(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
					if tc.networkExists {
						t.Error("NetworkCreate: want no-op when network already exists")
					}
					created = name
					return network.CreateResponse{}, nil
				},
			}
			ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
			if err != nil {
				t.Fatalf("NewWithClient: %v", err)
			}
			t.Cleanup(func() { _ = ctl.Close() })

			name, err := ctl.EnsureOrgNetwork(t.Context(), "acme")
			if err != nil {
				t.Fatalf("EnsureOrgNetwork: %v", err)
			}
			want := OrgNetworkName("acme")
			if name != want {
				t.Errorf("EnsureOrgNetwork returned %q, want %q", name, want)
			}
			if inspected != want {
				t.Errorf("inspected = %q, want %q", inspected, want)
			}
			if !tc.networkExists && created != want {
				t.Errorf("created = %q, want %q", created, want)
			}
		})
	}
}

// TestEnsureCaddyNetworkAliasForOrg verifies it ensures the org's own
// network exists and aliases Caddy on it, not the shared ownscaleNetwork.
func TestEnsureCaddyNetworkAliasForOrg(t *testing.T) {
	var connectedNetwork string
	api := &dockerfake.API{
		NetworkInspectFn: func(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
			return network.Inspect{}, cerrdefs.ErrNotFound
		},
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{{ID: "caddy-1"}}, nil
		},
		ContainerInspectFn: func(ctx context.Context, containerID string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "running"}},
				NetworkSettings:   &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{}},
			}, nil
		},
		NetworkConnectFn: func(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error {
			connectedNetwork = networkID
			return nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if err := ctl.EnsureCaddyNetworkAliasForOrg(t.Context(), "acme", "example.com"); err != nil {
		t.Fatalf("EnsureCaddyNetworkAliasForOrg: %v", err)
	}
	if want := OrgNetworkName("acme"); connectedNetwork != want {
		t.Fatalf("caddy connected to network %q, want %q", connectedNetwork, want)
	}
}

// TestRemoveOrganizationCleansUpNetwork verifies RemoveOrganization
// disconnects Caddy from the organization's network before removing it.
func TestRemoveOrganizationCleansUpNetwork(t *testing.T) {
	var disconnectedNetwork, disconnectedContainer string
	var removedNetwork string
	api := &dockerfake.API{
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{{ID: "caddy-1"}}, nil
		},
		NetworkDisconnectFn: func(ctx context.Context, networkID, containerID string, force bool) error {
			disconnectedNetwork, disconnectedContainer = networkID, containerID
			return nil
		},
		NetworkRemoveFn: func(ctx context.Context, networkID string) error {
			removedNetwork = networkID
			return nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if err := ctl.RemoveOrganization(t.Context(), "acme"); err != nil {
		t.Fatalf("RemoveOrganization: %v", err)
	}

	want := OrgNetworkName("acme")
	if disconnectedNetwork != want || disconnectedContainer != "caddy-1" {
		t.Fatalf("NetworkDisconnect(%q, %q), want (%q, %q)", disconnectedNetwork, disconnectedContainer, want, "caddy-1")
	}
	if removedNetwork != want {
		t.Fatalf("NetworkRemove(%q), want (%q)", removedNetwork, want)
	}
}

// TestRemoveOrganizationCleansUpConfigDir verifies RemoveOrganization also
// removes the host directory holding the shared headscale config file
// created by CreateOrganization (see headscaleConfigHostDir), so it
// doesn't leak on disk after an organization is deleted.
func TestRemoveOrganizationCleansUpConfigDir(t *testing.T) {
	dataDir := t.TempDir()
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
	}
	ctl, err := NewWithClient(api, dataDir, "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{}, config.DerpConfig{}, DefaultACLPolicy); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	configHostDir := ctl.headscaleConfigHostDir("acme")
	if _, err := os.Stat(filepath.Join(configHostDir, "config.yaml")); err != nil {
		t.Fatalf("config file should exist after CreateOrganization: %v", err)
	}

	if err := ctl.RemoveOrganization(t.Context(), "acme"); err != nil {
		t.Fatalf("RemoveOrganization: %v", err)
	}

	if _, err := os.Stat(configHostDir); !os.IsNotExist(err) {
		t.Fatalf("config directory = %v, want it removed by RemoveOrganization", err)
	}
}

// TestRemoveOrganizationIgnoresMissingNetwork verifies RemoveOrganization
// doesn't fail when the organization's network is already gone.
func TestRemoveOrganizationIgnoresMissingNetwork(t *testing.T) {
	api := &dockerfake.API{
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return nil, nil
		},
		NetworkRemoveFn: func(ctx context.Context, networkID string) error {
			return cerrdefs.ErrNotFound
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if err := ctl.RemoveOrganization(t.Context(), "acme"); err != nil {
		t.Fatalf("RemoveOrganization: want nil error when network already missing, got %v", err)
	}
}

// TestRemoveOrganizationDoesNotFailWhenNetworkRemovalErrors verifies
// RemoveOrganization is best-effort about network cleanup: an unexpected
// NetworkRemove failure (e.g. stray endpoints still attached) is logged,
// not returned, since the organization's containers/data are already gone.
func TestRemoveOrganizationDoesNotFailWhenNetworkRemovalErrors(t *testing.T) {
	api := &dockerfake.API{
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return nil, nil
		},
		NetworkRemoveFn: func(ctx context.Context, networkID string) error {
			return errors.New("network has active endpoints")
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if err := ctl.RemoveOrganization(t.Context(), "acme"); err != nil {
		t.Fatalf("RemoveOrganization: want nil error even when network removal fails, got %v", err)
	}
}

// TestEnsureOwnscaleNetworkForOrgNoContainerFound verifies it's a no-op
// (not an error) when the ownscale container itself can't be found (e.g.
// running outside docker-compose, as in most tests).
// TestEnsureOwnscaleNetworkForOrg covers the three states
// EnsureOwnscaleNetworkForOrg needs to handle: no running ownscale
// container at all, one found but not yet on the org's network (which
// must be created if missing and connected without a DNS alias, unlike
// Caddy's), and one already connected (a no-op).
func TestEnsureOwnscaleNetworkForOrg(t *testing.T) {
	netName := OrgNetworkName("acme")
	cases := []struct {
		name string
		// containerFound selects whether ContainerList (filtered by the
		// ownscale compose service label) finds a running container.
		containerFound bool
		// networkExists selects whether the org's network already
		// exists (NetworkInspect succeeds) or must be created.
		networkExists bool
		// alreadyConnected selects whether ownscale's ContainerInspect
		// reports it's already a member of the org's network.
		alreadyConnected bool
	}{
		{name: "no container found", containerFound: false, networkExists: true},
		{name: "connects when not attached", containerFound: true, networkExists: false, alreadyConnected: false},
		{name: "no-op when already connected", containerFound: true, networkExists: true, alreadyConnected: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var created bool
			var connectedNetwork, connectedContainer string
			var connectedConfig *network.EndpointSettings
			networks := map[string]*network.EndpointSettings{}
			if tc.alreadyConnected {
				networks[netName] = &network.EndpointSettings{}
			}
			api := &dockerfake.API{
				NetworkInspectFn: func(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
					if tc.networkExists {
						return network.Inspect{ID: netName}, nil
					}
					return network.Inspect{}, cerrdefs.ErrNotFound
				},
				NetworkCreateFn: func(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
					created = true
					return network.CreateResponse{ID: name}, nil
				},
				ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
					if !tc.containerFound || !options.Filters.ExactMatch("label", ownscaleComposeServiceLabel) {
						return nil, nil
					}
					return []container.Summary{{ID: "ownscale-1"}}, nil
				},
				ContainerInspectFn: func(ctx context.Context, containerID string) (container.InspectResponse, error) {
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "running"}},
						NetworkSettings:   &container.NetworkSettings{Networks: networks},
					}, nil
				},
				NetworkConnectFn: func(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error {
					if tc.alreadyConnected {
						t.Error("NetworkConnect: want no reconnect when ownscale is already on the network")
					}
					connectedNetwork, connectedContainer, connectedConfig = networkID, containerID, config
					return nil
				},
				NetworkDisconnectFn: func(ctx context.Context, networkID, containerID string, force bool) error {
					t.Error("NetworkDisconnect: want no disconnect (only reconnect-with-alias needs that, like Caddy)")
					return nil
				},
			}
			ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
			if err != nil {
				t.Fatalf("NewWithClient: %v", err)
			}
			t.Cleanup(func() { _ = ctl.Close() })

			if err := ctl.EnsureOwnscaleNetworkForOrg(t.Context(), "acme"); err != nil {
				t.Fatalf("EnsureOwnscaleNetworkForOrg: %v", err)
			}
			if !tc.containerFound {
				return
			}
			if !tc.networkExists && !created {
				t.Error("want NetworkCreate called when the org network doesn't exist yet")
			}
			if !tc.alreadyConnected {
				if connectedNetwork != netName || connectedContainer != "ownscale-1" {
					t.Errorf("NetworkConnect(%q, %q), want (%q, %q)", connectedNetwork, connectedContainer, netName, "ownscale-1")
				}
				if connectedConfig != nil && len(connectedConfig.Aliases) != 0 {
					t.Errorf("NetworkConnect aliases = %v, want none (ownscale doesn't need a DNS alias)", connectedConfig.Aliases)
				}
			}
		})
	}
}

// TestCreateOrganizationJoinsOwnscaleToOrgNetwork verifies CreateOrganization
// connects the ownscale container itself to the new organization's network
// (needed for headscaleclient to reach hs-<subdomain> directly),
// in addition to aliasing Caddy on it.
func TestCreateOrganizationJoinsOwnscaleToOrgNetwork(t *testing.T) {
	var ownscaleConnected bool
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			if options.Filters.ExactMatch("label", ownscaleComposeServiceLabel) {
				return []container.Summary{{ID: "ownscale-1"}}, nil
			}
			return nil, nil
		},
		ContainerInspectFn: func(ctx context.Context, containerID string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "running"}},
				NetworkSettings:   &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{}},
			}, nil
		},
		NetworkConnectFn: func(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error {
			if containerID == "ownscale-1" {
				ownscaleConnected = true
			}
			return nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{}, config.DerpConfig{}, DefaultACLPolicy); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if !ownscaleConnected {
		t.Fatal("CreateOrganization did not connect the ownscale container to the org network")
	}
}

// TestRemoveOrganizationDisconnectsOwnscaleFromNetwork verifies
// RemoveOrganization disconnects the ownscale container (in addition to
// Caddy) from the organization's network before removing it.
func TestRemoveOrganizationDisconnectsOwnscaleFromNetwork(t *testing.T) {
	var disconnectedContainers []string
	api := &dockerfake.API{
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			if options.Filters.ExactMatch("label", ownscaleComposeServiceLabel) {
				return []container.Summary{{ID: "ownscale-1"}}, nil
			}
			if options.Filters.ExactMatch("label", caddyComposeServiceLabel) {
				return []container.Summary{{ID: "caddy-1"}}, nil
			}
			return nil, nil
		},
		NetworkDisconnectFn: func(ctx context.Context, networkID, containerID string, force bool) error {
			disconnectedContainers = append(disconnectedContainers, containerID)
			return nil
		},
		NetworkRemoveFn: func(ctx context.Context, networkID string) error {
			return nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if err := ctl.RemoveOrganization(t.Context(), "acme"); err != nil {
		t.Fatalf("RemoveOrganization: %v", err)
	}
	if !slices.Contains(disconnectedContainers, "ownscale-1") {
		t.Fatalf("disconnected containers = %v, want it to contain %q", disconnectedContainers, "ownscale-1")
	}
	if !slices.Contains(disconnectedContainers, "caddy-1") {
		t.Fatalf("disconnected containers = %v, want it to contain %q", disconnectedContainers, "caddy-1")
	}
}

// TestMintHeadscaleAPIKeyByContainerName verifies MintHeadscaleAPIKey works
// when passed a container name (not just a freshly created container ID),
// since Registry mints ownscale's own key against an already-running
// organization's container, identified only by HSContainerName(subdomain).
func TestMintHeadscaleAPIKeyByContainerName(t *testing.T) {
	var execContainer string
	api := &dockerfake.API{
		ContainerExecCreateFn: func(ctx context.Context, containerID string, options container.ExecOptions) (container.ExecCreateResponse, error) {
			execContainer = containerID
			return container.ExecCreateResponse{ID: "exec-1"}, nil
		},
		ContainerExecAttachFn: func(ctx context.Context, execID string, options container.ExecAttachOptions) (types.HijackedResponse, error) {
			clientConn, serverConn := net.Pipe()
			go func() {
				w := stdcopy.NewStdWriter(serverConn, stdcopy.Stdout)
				_, _ = w.Write([]byte("ownscale-own-api-key\n"))
				_ = serverConn.Close()
			}()
			return types.NewHijackedResponse(clientConn, ""), nil
		},
		ContainerExecInspectFn: func(ctx context.Context, execID string) (container.ExecInspect, error) {
			return container.ExecInspect{ExitCode: 0}, nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	key, err := ctl.MintHeadscaleAPIKey(t.Context(), HSContainerName("acme"))
	if err != nil {
		t.Fatalf("MintHeadscaleAPIKey: %v", err)
	}
	if key != "ownscale-own-api-key" {
		t.Fatalf("key = %q, want %q", key, "ownscale-own-api-key")
	}
	if execContainer != HSContainerName("acme") {
		t.Fatalf("exec container = %q, want %q", execContainer, HSContainerName("acme"))
	}
}

// TestBuildHSConfigIncludesPolicy verifies the rendered headscale config
// points headscale at the uploaded ACL policy file in "file" mode.
func TestBuildHSConfigIncludesPolicy(t *testing.T) {
	cfg, err := BuildHeadscaleConfig("acme", "acme.example.com", OIDCClientCredentials{}, config.DerpConfig{})
	if err != nil {
		t.Fatalf("BuildHeadscaleConfig: %v", err)
	}
	for _, want := range []string{
		"policy:\n  mode: file\n  path: " + PolicyConfigPath,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}
}

// TestCreateOrganizationUploadsACLPolicy verifies CreateOrganization
// uploads the given ACL policy content into the headscale container's
// filesystem (not a bind mount) at PolicyConfigPath.
func TestCreateOrganizationUploadsACLPolicy(t *testing.T) {
	copied := make(map[string]map[string]string)
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		CopyToContainerFn: func(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error {
			files, err := tarContents(content)
			if err != nil {
				return err
			}
			if copied[containerID] == nil {
				copied[containerID] = make(map[string]string)
			}
			maps.Copy(copied[containerID], files)
			return nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	wantPolicy := `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}]}`
	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{}, config.DerpConfig{}, wantPolicy); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	got := copied[HSContainerName("acme")][strings.TrimPrefix(PolicyConfigPath, "/")]
	if got != wantPolicy {
		t.Fatalf("uploaded acl policy = %q, want %q", got, wantPolicy)
	}

	// No bind mount for the policy file: it must only reach the container
	// via CopyToContainer, source-of-truth stays in the DB, not the host fs.
	if _, err := os.Stat(filepath.Join(ctl.headscaleConfigHostDir("acme"), "acl.hujson")); err == nil {
		t.Fatalf("acl.hujson unexpectedly written to host config dir")
	}
}

// TestWriteACLPolicyUploadsToRunningContainer verifies WriteACLPolicy
// re-uploads content into the (already running) headscale container by
// name, without requiring a fresh container ID.
func TestWriteACLPolicyUploadsToRunningContainer(t *testing.T) {
	var gotContainerID string
	var gotContent string
	api := &dockerfake.API{
		CopyToContainerFn: func(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error {
			files, err := tarContents(content)
			if err != nil {
				return err
			}
			gotContainerID = containerID
			gotContent = files[strings.TrimPrefix(PolicyConfigPath, "/")]
			return nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	newPolicy := `{"acls":[]}`
	if err := ctl.WriteACLPolicy(t.Context(), "acme", newPolicy); err != nil {
		t.Fatalf("WriteACLPolicy: %v", err)
	}
	if gotContainerID != HSContainerName("acme") {
		t.Fatalf("container = %q, want %q", gotContainerID, HSContainerName("acme"))
	}
	if gotContent != newPolicy {
		t.Fatalf("content = %q, want %q", gotContent, newPolicy)
	}
}

// TestRestartHeadscaleRestartsTheRightContainer verifies RestartHeadscale
// restarts only the org's headscale container (not headplane).
func TestRestartHeadscaleRestartsTheRightContainer(t *testing.T) {
	var restarted string
	api := &dockerfake.API{
		ContainerRestartFn: func(ctx context.Context, containerID string, options container.StopOptions) error {
			restarted = containerID
			return nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if err := ctl.RestartHeadscale(t.Context(), "acme"); err != nil {
		t.Fatalf("RestartHeadscale: %v", err)
	}
	if restarted != HSContainerName("acme") {
		t.Fatalf("restarted = %q, want %q", restarted, HSContainerName("acme"))
	}
}

// newProvisionedTestCtl provisions organization "acme" (so its
// config.yaml exists on disk to patch) and returns a Ctl plus a pointer to
// the list of restarted container names.
func newProvisionedTestCtl(t *testing.T) (ctl *Ctl, restarted *[]string) {
	t.Helper()
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
	var err error
	ctl, err = NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{
		Issuer: "https://auth.example.com", ClientID: "client-id", ClientSecret: "client-secret",
	}, config.DerpConfig{}, DefaultACLPolicy); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	restartedContainers = nil // ignore CreateOrganization's own container start, only ContainerRestart is tracked anyway
	return ctl, &restartedContainers
}
