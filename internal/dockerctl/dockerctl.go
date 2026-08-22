// Package dockerctl manages per-subdomain headscale containers via the Docker SDK.
package dockerctl

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"

	"github.com/ownscalenet/ownscale/internal/derrors"
)

const (
	ownscaleNetwork     = "os-net"
	Image               = "headscale/headscale:0.29.3"
	headplaneImage      = "ghcr.io/tale/headplane:0.7.0-shell"
	headscaleConfigPath = "/etc/headscale/config.yaml"
	headplaneConfigPath = "/etc/headplane/config.yaml"
)

func HSContainerName(subdomain string) string { return "hs-" + subdomain }
func HPContainerName(subdomain string) string { return "hp-" + subdomain }
func HPVolumeName(subdomain string) string    { return "hp-" + subdomain + "-data" }
func HSExternalEndpoint(domain string) string { return "https://" + domain }

// API is the subset of the Docker Engine API that Ctl depends on.
// The intention behind making this interface is to allow tests in other packages
// to use Ctl implementations, not fakes/mocks. That way tests test more.
type API interface {
	client.ContainerAPIClient
	client.NetworkAPIClient
	client.VolumeAPIClient
	io.Closer
}

var _ API = (*client.Client)(nil)

type Ctl struct {
	cli     API
	dataDir string
	logger  *slog.Logger

	// ctx/cancel bound background log-streaming goroutines (see logs.go)
	// to Ctl's own lifetime rather than to any single request's context,
	// since streaming must keep running after the call that started a
	// container returns, and must stop cleanly on Close.
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a controller using the standard Docker environment (DOCKER_HOST
// etc.). dataDir must be an absolute, host-real directory: it's used as the
// base for the "headscale/<subdomain>" bind-mount source handed to the
// Docker API when creating each organization's headscale container (see
// headscaleHostDir and CreateOrganization), and that source is resolved by
// the Docker daemon, not by this process's own filesystem view (see
// Config.DataDir's doc comment in internal/config for why that matters).
func New(dataDir string, logger *slog.Logger) (_ *Ctl, err error) {
	defer derrors.Wrap(&err, "dockerctl.New(%q)", dataDir)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return NewWithClient(cli, dataDir, logger)
}

// NewWithClient creates a controller backed by an arbitrary API
// implementation. It's exposed primarily so tests can inject a fake (see
// internal/dockerctl/dockerfake) without a real Docker daemon; production
// code should use New.
func NewWithClient(cli API, dataDir string, logger *slog.Logger) (_ *Ctl, err error) {
	defer derrors.Wrap(&err, "dockerctl.NewWithClient(dataDir=%q)", dataDir)

	if !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("dataDir must be an absolute path, got %q", dataDir)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Ctl{cli: cli, dataDir: dataDir, logger: logger, ctx: ctx, cancel: cancel}, nil
}

// headscaleHostDir returns the host directory bind-mounted at
// /var/lib/headscale in an organization's headscale container. Subdomains
// are validated as safe DNS labels by ValidateSubdomain before this is
// called, so there's no path-traversal concern.
func (c *Ctl) headscaleHostDir(subdomain string) string {
	return filepath.Join(c.dataDir, "headscale", subdomain)
}

func (c *Ctl) Close() (err error) {
	defer derrors.Wrap(&err, "c.Close()")
	c.cancel()
	return c.cli.Close()
}

// EnsureNetwork creates the shared network if it does not exist.
func (c *Ctl) EnsureNetwork(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "c.EnsureNetwork(ctx)")

	_, err = c.cli.NetworkInspect(ctx, ownscaleNetwork, network.InspectOptions{})
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return err
	}
	_, err = c.cli.NetworkCreate(ctx, ownscaleNetwork, network.CreateOptions{})
	return err
}

// headscale config for an organization; ServerURL must match the public subdomain.
var configTemplateHS = template.Must(template.New("headscaleConfig").Parse(`server_url: https://{{.ServerURL}}
listen_addr: 0.0.0.0:8080
metrics_listen_addr: 127.0.0.1:9090

noise:
  private_key_path: /var/lib/headscale/noise_private.key

prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
  allocation: sequential

derp:
  urls:
    - https://controlplane.tailscale.com/derpmap/default
  auto_update_enabled: true
  update_frequency: 3h
  server:
    enabled: false

database:
  type: sqlite
  sqlite:
    path: /var/lib/headscale/db.sqlite
    write_ahead_log: true

log:
  level: info
  format: text

dns:
  magic_dns: true
  base_domain: {{.Subdomain}}.headscale.internal
  nameservers:
    global:
      - 1.1.1.1

unix_socket: /var/run/headscale/headscale.sock
unix_socket_permission: "0770"
`))

// buildHSConfig renders the headscale config.yaml content for an organization.
func buildHSConfig(subdomain, domain string) (_ string, err error) {
	defer derrors.Wrap(&err, "buildHSConfig(%q, %q)", subdomain, domain)

	var buf bytes.Buffer
	if err := configTemplateHS.Execute(&buf, struct{ Subdomain, ServerURL string }{
		Subdomain: subdomain,
		ServerURL: domain,
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// headplane config for an organization; BaseURL must be the public
// subdomain URL (without the /admin dashboard prefix), and headscale.url
// points at the organization's own headscale container over the shared
// Docker network. Advanced features (Docker/Kubernetes/proc integrations)
// are left disabled since they'd require host access (Docker socket, shared
// headscale config file) we don't grant per organization; login instead
// uses a Headscale API key pasted into the Headplane UI ("Limited Mode").
var configTemplateHP = template.Must(template.New("headplaneConfig").Parse(`server:
  host: "0.0.0.0"
  port: 3000
  base_url: "https://{{.BaseURL}}"
  cookie_secret: "{{.CookieSecret}}"
  cookie_secure: true
  data_path: "/var/lib/headplane"

headscale:
  url: "http://{{.HSContainerName}}:8080"
  public_url: "{{.HSExternalEndpoint}}"

integration:
  agent:
    enabled: false
  docker:
    enabled: false
  kubernetes:
    enabled: false
    validate_manifest: false
    pod_name: "unused"
  proc:
    enabled: false
`))

// buildHPConfig renders the headplane config.yaml content for an
// organization.
func buildHPConfig(subdomain, domain, cookieSecret string) (_ string, err error) {
	defer derrors.Wrap(&err, "buildHPConfig(%q, %q, %q)", subdomain, domain, cookieSecret)

	var buf bytes.Buffer
	if err := configTemplateHP.Execute(&buf, struct{ BaseURL, CookieSecret, HSContainerName, HSExternalEndpoint string }{
		BaseURL:            domain,
		CookieSecret:       cookieSecret,
		HSContainerName:    HSContainerName(subdomain),
		HSExternalEndpoint: HSExternalEndpoint(domain),
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// randomCookieSecret generates a 32-character hex string suitable for
// headplane's server.cookie_secret.
func randomCookieSecret() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// configTar wraps content in a tar archive containing a single file at path
// (an absolute container path, e.g. "/etc/headscale/config.yaml"), along
// with explicit directory entries for every intermediate path component.
// It's meant to be extracted at the container's filesystem root ("/") via
// client.CopyToContainer: some minimal images (e.g. distroless-style
// headscale/headplane images) don't ship the config directory pre-created,
// and CopyToContainer requires its destination directory to already exist,
// so we can't extract directly into headscaleConfigDir/headplaneConfigDir.
// Rooting the archive at "/" (which always exists) and including directory
// entries lets the daemon's tar extraction create any missing directories.
func configTar(path, content string) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	rel := strings.TrimPrefix(path, "/")
	dir := ""
	for _, part := range strings.Split(filepath.Dir(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		dir += part + "/"
		if err := tw.WriteHeader(&tar.Header{
			Name:     dir,
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		}); err != nil {
			return nil, err
		}
	}

	if err := tw.WriteHeader(&tar.Header{
		Name: rel,
		Mode: 0o644,
		Size: int64(len(content)),
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

// CreateOrganization provisions and starts a headscale container serving
// https://<subdomain>.<baseDomain>. It returns the container ID.
func (c *Ctl) CreateOrganization(ctx context.Context, subdomain, baseDomain string) (_ string, err error) {
	defer derrors.Wrap(&err, "c.CreateOrganization(%q, %q)", subdomain, baseDomain)

	if err := ValidateSubdomain(subdomain); err != nil {
		return "", err
	}
	if err := c.EnsureNetwork(ctx); err != nil {
		return "", fmt.Errorf("ensure network: %w", err)
	}

	domain := subdomain + "." + baseDomain
	cfg, err := buildHSConfig(subdomain, domain)
	if err != nil {
		return "", fmt.Errorf("build config: %w", err)
	}

	hostDir := c.headscaleHostDir(subdomain)
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}

	resp, err := c.cli.ContainerCreate(ctx,
		&container.Config{
			Image: Image,
			Cmd:   []string{"serve"},
			Labels: map[string]string{
				"ownscale.organization": "true",
				"ownscale.subdomain":    subdomain,
				"ownscale.domain":       domain,
			},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: hostDir, Target: "/var/lib/headscale"},
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				ownscaleNetwork: {},
			},
		},
		nil,
		HSContainerName(subdomain),
	)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	tarBuf, err := configTar(headscaleConfigPath, cfg)
	if err != nil {
		return "", fmt.Errorf("build config archive: %w", err)
	}
	if err := c.cli.CopyToContainer(ctx, resp.ID, "/", tarBuf, container.CopyToContainerOptions{}); err != nil {
		return "", fmt.Errorf("copy config into container: %w", err)
	}

	if err := c.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}
	go c.streamLogs(resp.ID, HSContainerName(subdomain))

	if err := c.createHeadplane(ctx, subdomain, domain); err != nil {
		return "", fmt.Errorf("create headplane: %w", err)
	}

	return resp.ID, nil
}

// createHeadplane provisions and starts the headplane container for an
// organization, configured to talk to that organization's headscale
// container over the shared Docker network.
func (c *Ctl) createHeadplane(ctx context.Context, subdomain, domain string) (err error) {
	defer derrors.Wrap(&err, "c.createHeadplane(%q, %q)", subdomain, domain)

	cookieSecret, err := randomCookieSecret()
	if err != nil {
		return fmt.Errorf("generate cookie secret: %w", err)
	}
	cfg, err := buildHPConfig(subdomain, domain, cookieSecret)
	if err != nil {
		return fmt.Errorf("build headplane config: %w", err)
	}

	volName := HPVolumeName(subdomain)
	if _, err := c.cli.VolumeCreate(ctx, volume.CreateOptions{Name: volName}); err != nil {
		return fmt.Errorf("create volume: %w", err)
	}

	resp, err := c.cli.ContainerCreate(ctx,
		&container.Config{
			Image: headplaneImage,
			Labels: map[string]string{
				"ownscale.organization": "true",
				"ownscale.subdomain":    subdomain,
				"ownscale.domain":       domain,
				"ownscale.component":    "headplane",
			},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: volName, Target: "/var/lib/headplane"},
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				ownscaleNetwork: {},
			},
		},
		nil,
		HPContainerName(subdomain),
	)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	tarBuf, err := configTar(headplaneConfigPath, cfg)
	if err != nil {
		return fmt.Errorf("build config archive: %w", err)
	}
	if err := c.cli.CopyToContainer(ctx, resp.ID, "/", tarBuf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copy config into container: %w", err)
	}

	if err := c.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	go c.streamLogs(resp.ID, HPContainerName(subdomain))
	return nil
}

// RemoveOrganization stops and removes the headscale and headplane
// containers and their data (the headscale host directory under DataDir,
// and headplane's Docker volume). Missing resources are ignored so removal
// is idempotent.
func (c *Ctl) RemoveOrganization(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "c.RemoveOrganization(%q)", subdomain)

	name := HSContainerName(subdomain)
	rmErr := c.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if rmErr != nil && !cerrdefs.IsNotFound(rmErr) {
		return fmt.Errorf("remove container: %w", rmErr)
	}
	if err := os.RemoveAll(c.headscaleHostDir(subdomain)); err != nil {
		return fmt.Errorf("remove data directory: %w", err)
	}

	hpName := HPContainerName(subdomain)
	if err := c.cli.ContainerRemove(ctx, hpName, container.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove headplane container: %w", err)
	}
	if err := c.cli.VolumeRemove(ctx, HPVolumeName(subdomain), false); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove headplane volume: %w", err)
	}
	return nil
}

// StartOrganization starts the headscale and headplane containers for
// subdomain if they exist and aren't already running. It's the counterpart
// to StopAll/RemoveOrganization: it never (re)creates containers or
// configs, it only starts pre-existing ones. Missing containers are
// reported as an error, since that indicates the organization was never
// provisioned (or was removed) rather than just stopped.
func (c *Ctl) StartOrganization(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "c.StartOrganization(%q)", subdomain)

	if err := c.cli.ContainerStart(ctx, HSContainerName(subdomain), container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	go c.streamLogs(HSContainerName(subdomain), HSContainerName(subdomain))
	if err := c.cli.ContainerStart(ctx, HPContainerName(subdomain), container.StartOptions{}); err != nil {
		return fmt.Errorf("start headplane container: %w", err)
	}
	go c.streamLogs(HPContainerName(subdomain), HPContainerName(subdomain))
	return nil
}

// ContainerState returns the container state string (e.g. "running") or
// "missing" when the container does not exist.
func (c *Ctl) ContainerState(ctx context.Context, subdomain string) (_ string, err error) {
	defer derrors.Wrap(&err, "c.ContainerState(%q)", subdomain)

	info, err := c.cli.ContainerInspect(ctx, HSContainerName(subdomain))
	if cerrdefs.IsNotFound(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	return info.State.Status, nil
}

// StopAll gracefully stops every headscale and headplane container managed
// by ownscale (identified by "ownscale.organization=true" label)
// restarts/deploys. This function will not fail-fast.
func (c *Ctl) StopAll(ctx context.Context, timeout time.Duration) (err error) {
	defer derrors.Wrap(&err, "c.StopAll(%s)", timeout)

	f := filters.NewArgs(filters.Arg("label", "ownscale.organization=true"))
	containers, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	timeoutSeconds := int(timeout.Seconds())
	var errs []error
	for _, ct := range containers {
		name := strings.TrimPrefix(strings.Join(ct.Names, ","), "/")
		stopErr := c.cli.ContainerStop(ctx, ct.ID, container.StopOptions{Timeout: &timeoutSeconds})
		if stopErr != nil && !cerrdefs.IsNotFound(stopErr) {
			errs = append(errs, fmt.Errorf("stop %s: %w", name, stopErr))
		}
	}
	return errors.Join(errs...)
}

var subdomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// reservedSubdomains are single DNS labels that organizations may not use,
// because they're reserved for fixed, non-organization Caddy site blocks
// (e.g. "admin" serves the ownscale admin panel at admin.<base_domain>).
var reservedSubdomains = map[string]bool{
	"admin": true,
	"www":   true,
}

// ValidateSubdomain checks that s is a valid single DNS label and not one of
// the reserved names used by fixed, non-organization site blocks.
func ValidateSubdomain(s string) (err error) {
	defer derrors.Wrap(&err, "dockerctl.ValidateSubdomain(%q)", s)

	if !subdomainRe.MatchString(s) {
		return fmt.Errorf("must be a DNS label (lowercase letters, digits, hyphens)")
	}
	if len(s) < 4 {
		return fmt.Errorf("must be at least 4 characters")
	}
	if reservedSubdomains[strings.ToLower(s)] {
		return fmt.Errorf("%q is a reserved subdomain and cannot be used for an organization", s)
	}
	return nil
}
