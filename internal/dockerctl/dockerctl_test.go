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

	"github.com/ownscalenet/ownscale/internal/config"
	"github.com/ownscalenet/ownscale/internal/dockerctl/dockerfake"
	"github.com/ownscalenet/ownscale/internal/localca"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBuildHSConfigIncludesOIDC(t *testing.T) {
	cfg, err := buildHSConfig("acme", "acme.example.com", OIDCClientCredentials{
		Issuer:       "https://auth.example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, config.DerpConfig{})
	if err != nil {
		t.Fatalf("buildHSConfig: %v", err)
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
	cfg, err := buildHSConfig("acme", "acme.example.com", OIDCClientCredentials{}, config.DerpConfig{
		Hostname:   "derp.example.com",
		STUNPort:   3478,
		RegionID:   900,
		RegionCode: "custom",
		RegionName: "Self-hosted DERP",
	})
	if err != nil {
		t.Fatalf("buildHSConfig: %v", err)
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
// and points it at the me.tale.headplane.target label headscale is
// created with, and the Docker socket headplane's container gets
// bind-mounted (see createHeadplane).
func TestBuildHPConfigEnablesDockerIntegration(t *testing.T) {
	cfg, err := buildHPConfig("acme", "acme.example.com", "cookie-secret", "headscale-api-key", OIDCClientCredentials{})
	if err != nil {
		t.Fatalf("buildHPConfig: %v", err)
	}
	for _, want := range []string{
		"docker:\n    enabled: true",
		`container_label: "me.tale.headplane.target=headscale"`,
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

	_, err = ctl.CreateOrganization(context.Background(), "acme", "example.com", OIDCClientCredentials{
		Issuer:       "https://auth.example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, config.DerpConfig{})
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

func TestEnsureCaddyNetworkAliasNoCaddyFound(t *testing.T) {
	api := &dockerfake.API{}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if err := ctl.ensureCaddyNetworkAlias(t.Context(), ownscaleNetwork, "example.com"); err != nil {
		t.Fatalf("ensureCaddyNetworkAlias: want nil error when caddy isn't found, got %v", err)
	}
}

func TestEnsureCaddyNetworkAliasConnectsWhenNotAttached(t *testing.T) {
	var connectedNetwork, connectedContainer string
	var connectedConfig *network.EndpointSettings
	api := &dockerfake.API{
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
			connectedNetwork, connectedContainer, connectedConfig = networkID, containerID, config
			return nil
		},
		NetworkDisconnectFn: func(ctx context.Context, networkID, containerID string, force bool) error {
			t.Fatal("NetworkDisconnect: want no disconnect when caddy isn't yet on the network")
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
	if connectedNetwork != ownscaleNetwork || connectedContainer != "caddy-1" {
		t.Fatalf("NetworkConnect(%q, %q), want (%q, %q)", connectedNetwork, connectedContainer, ownscaleNetwork, "caddy-1")
	}
	if connectedConfig == nil || !slices.Contains(connectedConfig.Aliases, "example.com") {
		t.Fatalf("NetworkConnect aliases = %v, want it to contain %q", connectedConfig, "example.com")
	}
}

func TestEnsureCaddyNetworkAliasReconnectsWhenAliasMissing(t *testing.T) {
	var disconnected, reconnected bool
	var newAliases []string
	api := &dockerfake.API{
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{{ID: "caddy-1"}}, nil
		},
		ContainerInspectFn: func(ctx context.Context, containerID string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "running"}},
				NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
					ownscaleNetwork: {Aliases: []string{"caddy"}},
				}},
			}, nil
		},
		NetworkDisconnectFn: func(ctx context.Context, networkID, containerID string, force bool) error {
			disconnected = true
			return nil
		},
		NetworkConnectFn: func(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error {
			reconnected = true
			newAliases = config.Aliases
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
	if !disconnected || !reconnected {
		t.Fatalf("disconnected=%v reconnected=%v, want both true", disconnected, reconnected)
	}
	if !slices.Contains(newAliases, "example.com") || !slices.Contains(newAliases, "caddy") {
		t.Fatalf("reconnect aliases = %v, want it to contain both %q and %q", newAliases, "example.com", "caddy")
	}
}

func TestEnsureCaddyNetworkAliasNoopWhenAlreadyAliased(t *testing.T) {
	api := &dockerfake.API{
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{{ID: "caddy-1"}}, nil
		},
		ContainerInspectFn: func(ctx context.Context, containerID string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "running"}},
				NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
					ownscaleNetwork: {Aliases: []string{"example.com"}},
				}},
			}, nil
		},
		NetworkConnectFn: func(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error {
			t.Fatal("NetworkConnect: want no-op when already aliased")
			return nil
		},
		NetworkDisconnectFn: func(ctx context.Context, networkID, containerID string, force bool) error {
			t.Fatal("NetworkDisconnect: want no-op when already aliased")
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
	}, config.DerpConfig{}); err != nil {
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
	}, config.DerpConfig{}); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	for _, mounts := range [][]mount.Mount{hsMounts, hpMounts} {
		if slices.ContainsFunc(mounts, func(m mount.Mount) bool { return m.Target == LocalCAMountTarget }) {
			t.Fatalf("mounts = %+v, want no local CA mount when LocalCADir is unset", mounts)
		}
	}
}

// TestCreateOrganizationLabelsHeadscaleForHeadplaneDiscovery verifies the
// headscale container gets the "me.tale.headplane.target=headscale" label,
// which is the exact label Headplane's Docker integration looks for by
// default (integration.docker.container_label) to find and restart it.
func TestCreateOrganizationLabelsHeadscaleForHeadplaneDiscovery(t *testing.T) {
	var hsLabels map[string]string
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			if containerName == HSContainerName("acme") {
				hsLabels = config.Labels
			}
			return container.CreateResponse{ID: containerName}, nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{}, config.DerpConfig{}); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	if got := hsLabels["me.tale.headplane.target"]; got != "headscale" {
		t.Fatalf(`headscale labels = %+v, want "me.tale.headplane.target": "headscale"`, hsLabels)
	}
}

// TestCreateOrganizationSharesHeadscaleConfigWithHeadplane verifies that
// the headscale config file is bind-mounted (read-only) into the headscale
// container and the exact same host file is bind-mounted (read-write) into
// the headplane container, both at headscaleConfigPath -- this is what
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
	}, config.DerpConfig{}); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	wantSource := filepath.Join(ctl.headscaleConfigHostDir("acme"), "config.yaml")

	hsIdx := slices.IndexFunc(hsMounts, func(m mount.Mount) bool { return m.Target == headscaleConfigPath })
	if hsIdx < 0 {
		t.Fatalf("headscale mounts = %+v, want a bind mount at %q", hsMounts, headscaleConfigPath)
	}
	if hsMounts[hsIdx].Source != wantSource || !hsMounts[hsIdx].ReadOnly {
		t.Fatalf("headscale config mount = %+v, want read-only bind mount of %q", hsMounts[hsIdx], wantSource)
	}

	hpIdx := slices.IndexFunc(hpMounts, func(m mount.Mount) bool { return m.Target == headscaleConfigPath })
	if hpIdx < 0 {
		t.Fatalf("headplane mounts = %+v, want a bind mount at %q", hpMounts, headscaleConfigPath)
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

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{}, config.DerpConfig{}); err != nil {
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

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{}, config.DerpConfig{}); err != nil {
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

func TestEnsureOrgNetworkCreatesIfMissing(t *testing.T) {
	var inspected, created string
	api := &dockerfake.API{
		NetworkInspectFn: func(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
			inspected = networkID
			return network.Inspect{}, cerrdefs.ErrNotFound
		},
		NetworkCreateFn: func(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
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
		t.Fatalf("EnsureOrgNetwork returned %q, want %q", name, want)
	}
	if inspected != want || created != want {
		t.Fatalf("inspected=%q created=%q, want both %q", inspected, created, want)
	}
}

func TestEnsureOrgNetworkNoopWhenAlreadyExists(t *testing.T) {
	api := &dockerfake.API{
		NetworkInspectFn: func(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
			return network.Inspect{}, nil
		},
		NetworkCreateFn: func(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
			t.Fatal("NetworkCreate: want no-op when network already exists")
			return network.CreateResponse{}, nil
		},
	}
	ctl, err := NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if _, err := ctl.EnsureOrgNetwork(t.Context(), "acme"); err != nil {
		t.Fatalf("EnsureOrgNetwork: %v", err)
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

	if _, err := ctl.CreateOrganization(t.Context(), "acme", "example.com", OIDCClientCredentials{}, config.DerpConfig{}); err != nil {
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
