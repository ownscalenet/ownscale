package dockerctl

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

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
	})
	if err != nil {
		t.Fatalf("buildHSConfig: %v", err)
	}
	for _, want := range []string{
		`issuer: "https://auth.example.com"`,
		`client_id: "client-id"`,
		`client_secret: "client-secret"`,
		`method: S256`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
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
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	hpCfg := copied[HPContainerName("acme")][strings.TrimPrefix(headplaneConfigPath, "/")]
	if !strings.Contains(hpCfg, `api_key: "headscale-api-key"`) || !strings.Contains(hpCfg, `client_id: "client-id"`) {
		t.Fatalf("unexpected headplane config:\n%s", hpCfg)
	}
	hsCfg := copied[HSContainerName("acme")][strings.TrimPrefix(headscaleConfigPath, "/")]
	if !strings.Contains(hsCfg, `issuer: "https://auth.example.com"`) {
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
	}); err != nil {
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
	}); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	for _, mounts := range [][]mount.Mount{hsMounts, hpMounts} {
		if slices.ContainsFunc(mounts, func(m mount.Mount) bool { return m.Target == LocalCAMountTarget }) {
			t.Fatalf("mounts = %+v, want no local CA mount when LocalCADir is unset", mounts)
		}
	}
}
