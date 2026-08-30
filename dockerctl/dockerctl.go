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
	"slices"
	"strings"
	"text/template"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"gopkg.in/yaml.v3"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/headscaleclient"
	"github.com/ownscalenet/ownscale/localca"
)

const (
	ownscaleNetwork     = "os-net"
	Image               = "headscale/headscale:0.29.3-debug"
	headplaneImage      = "ghcr.io/tale/headplane:0.7.0-shell"
	headscaleConfigPath = "/etc/headscale/config.yaml"
	headplaneConfigPath = "/etc/headplane/config.yaml"
	derpConfigPath      = "/etc/headscale/derp.yaml"
	PolicyConfigPath    = "/etc/headscale/acl.hujson"

	// Caddy self-signed CA is mounted here for organization containers.
	LocalCAMountTarget = "/etc/ownscale-local-ca"
)

func HSContainerName(subdomain string) string { return "hs-" + subdomain }
func HPContainerName(subdomain string) string { return "hp-" + subdomain }
func HSExternalEndpoint(domain string) string { return "https://" + domain }

// OrgNetworkName returns the name of the Docker network isolating a single
// organization's headscale/headplane containers from every other
// organization's. Each organization gets its own network (created lazily by
// EnsureOrgNetwork), separate from the shared ownscaleNetwork used by the
// core infra services (ownscale, dex, litestream, caddy).
func OrgNetworkName(subdomain string) string { return "org-" + subdomain + "-net" }

// OIDCClientCredentials are the per-organization relying-party credentials
// templated into headscale and headplane.
type OIDCClientCredentials struct {
	Issuer       string
	ClientID     string
	ClientSecret string
}

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

	// localCADir mirrors Config.LocalCADir: the host directory containing
	// the pre-generated local development root CA (see localca),
	// bind-mounted read-only at LocalCAMountTarget into every organization
	// container. Empty means "don't mount anything" -- e.g. if the config
	// value was cleared entirely, though in practice it always defaults to
	// a real path (see config.Load).
	localCADir string
}

// New creates a controller using the standard Docker environment (DOCKER_HOST
// etc.). dataDir must be an absolute, host-real directory: it's used as the
// base for the "headscale/<subdomain>" bind-mount source handed to the
// Docker API when creating each organization's headscale container (see
// headscaleHostDir and CreateOrganization), and that source is resolved by
// the Docker daemon, not by this process's own filesystem view (see
// Config.DataDir's doc comment in config for why that matters).
// localCADir should be Config.LocalCADir.
func New(dataDir, localCADir string, logger *slog.Logger) (_ *Ctl, err error) {
	defer derrors.Wrap(&err, "dockerctl.New(%q)", dataDir)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return NewWithClient(cli, dataDir, localCADir, logger)
}

// NewWithClient creates a controller backed by an arbitrary API
// implementation. It's exposed primarily so tests can inject a fake (see
// dockerctl/dockerfake) without a real Docker daemon; production
// code should use New.
func NewWithClient(cli API, dataDir, localCADir string, logger *slog.Logger) (_ *Ctl, err error) {
	defer derrors.Wrap(&err, "dockerctl.NewWithClient(dataDir=%q)", dataDir)

	if !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("dataDir must be an absolute path, got %q", dataDir)
	}
	// TODO: instantiating background context here seems suspicious.
	// Story is we need two contexts in this code:
	// 1. for making regular docker requests (create container, stop container, etc.)
	// 2. for long-running watcher for logs of containers from 1.
	ctx, cancel := context.WithCancel(context.Background())
	return &Ctl{cli: cli, dataDir: dataDir, logger: logger, ctx: ctx, cancel: cancel, localCADir: localCADir}, nil
}

// localCAMount returns the read-only bind mount attaching localCADir at
// LocalCAMountTarget, and true, or a zero Mount and false if no local CA
// directory is configured. Added to every organization container so they
// can trust the local development root CA Caddy signs its local certs from
func (c *Ctl) localCAMount() (mount.Mount, bool) {
	if c.localCADir == "" {
		return mount.Mount{}, false
	}
	return mount.Mount{Type: mount.TypeBind, Source: c.localCADir, Target: LocalCAMountTarget, ReadOnly: true}, true
}

// headscaleHostDir returns the host directory bind-mounted at
// /var/lib/headscale in an organization's headscale container. Subdomains
// are validated as safe DNS labels by ValidateSubdomain before this is
// called, so there's no path-traversal concern.
func (c *Ctl) headscaleHostDir(subdomain string) string {
	return filepath.Join(c.dataDir, "headscale", subdomain)
}

// headplaneHostDir returns the host directory bind-mounted at
// /var/lib/headplane in an organization's headplane container, mirroring
// headscaleHostDir so headplane's database is discovered and replicated by
// litestream's directory watcher the same way headscale's is.
func (c *Ctl) headplaneHostDir(subdomain string) string {
	return filepath.Join(c.dataDir, "headplane", subdomain)
}

// headscaleConfigHostDir returns the host directory for organization's
// headscale config.yaml, bind-mounted at headscaleConfigPath
// into both the headscale container (read-only) and the headplane
// container (read-write) so Headplane's Network Management feature can edit
// live headscale settings (DNS, MagicDNS, ACLs, ...) and have headscale
// pick them up on restart.
func (c *Ctl) headscaleConfigHostDir(subdomain string) string {
	return filepath.Join(c.dataDir, "headscale-config", subdomain)
}

func (c *Ctl) Close() (err error) {
	defer derrors.Wrap(&err, "c.Close()")
	c.cancel()
	return c.cli.Close()
}

// EnsureNetwork creates the shared network if it does not exist.
func (c *Ctl) EnsureNetwork(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "c.EnsureNetwork()")
	return c.ensureNetwork(ctx, ownscaleNetwork)
}

// EnsureOrgNetwork creates subdomain's isolated per-organization network if
// it does not exist, and returns its name. Every organization gets its own
// network (see OrgNetworkName), separate from the shared ownscaleNetwork,
// so that organizations' headscale/headplane containers can't reach each
// other over Docker's embedded network.
func (c *Ctl) EnsureOrgNetwork(ctx context.Context, subdomain string) (_ string, err error) {
	defer derrors.Wrap(&err, "c.EnsureOrgNetwork(%q)", subdomain)

	name := OrgNetworkName(subdomain)
	if err := c.ensureNetwork(ctx, name); err != nil {
		return "", err
	}
	return name, nil
}

// ensureNetwork creates the Docker network named name if it does not already exist.
func (c *Ctl) ensureNetwork(ctx context.Context, name string) (err error) {
	defer derrors.Wrap(&err, "c.ensureNetwork(%q)", name)

	_, err = c.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return err
	}
	_, err = c.cli.NetworkCreate(ctx, name, network.CreateOptions{})
	return err
}

// EnsureCaddyNetworkAlias re-asserts baseDomain as a network alias for the
// Caddy container on the shared network (see ensureCaddyNetworkAlias). It's
// exported so callers can reconcile this on ownscale process startup, not
// just when a new organization is created: the alias is set imperatively
// via the Docker API rather than declared in docker-compose.yml, so it does
// NOT survive Caddy's container being recreated (image rebuild, `docker
// compose up`, etc.) -- only the container being merely restarted keeps it.
// Without this, every org container created before such a recreation would
// silently lose the ability to resolve https://{baseDomain} to Caddy (it
// falls back to baseDomain's public DNS record, if any, which typically
// points nowhere useful from inside the container), breaking their OIDC
// login (discovery, and especially the token endpoint fetch) until the
// alias is restored.
func (c *Ctl) EnsureCaddyNetworkAlias(ctx context.Context, baseDomain string) (err error) {
	defer derrors.Wrap(&err, "c.EnsureCaddyNetworkAlias(%q)", baseDomain)

	if err := c.EnsureNetwork(ctx); err != nil {
		return err
	}
	return c.ensureCaddyNetworkAlias(ctx, ownscaleNetwork, baseDomain)
}

// EnsureCaddyNetworkAliasForOrg re-asserts baseDomain as a network alias
// for the Caddy container on subdomain's own per-organization network (see
// EnsureOrgNetwork and ensureCaddyNetworkAlias), so that subdomain's
// headscale/headplane containers -- which live only on their own isolated
// network, not the shared ownscaleNetwork -- can still resolve
// https://{baseDomain} back to Caddy for OIDC discovery/token calls.
//
// Like EnsureCaddyNetworkAlias, it's exported so callers can reconcile this
// on ownscale process startup (per organization) and not just when that
// organization is first created, since the alias doesn't survive Caddy's
// container being recreated.
func (c *Ctl) EnsureCaddyNetworkAliasForOrg(ctx context.Context, subdomain, baseDomain string) (err error) {
	defer derrors.Wrap(&err, "c.EnsureCaddyNetworkAliasForOrg(%q, %q)", subdomain, baseDomain)

	netName, err := c.EnsureOrgNetwork(ctx, subdomain)
	if err != nil {
		return err
	}
	return c.ensureCaddyNetworkAlias(ctx, netName, baseDomain)
}

// caddyComposeServiceLabel is the label Docker Compose sets on every
// container it creates for a given service, e.g. "caddy" for the compose.yml
// "caddy:" service. It's used (rather than a container name, which varies by
// Compose project name) to reliably find the Caddy container.
const caddyComposeServiceLabel = "com.docker.compose.service=caddy"

// ownscaleComposeServiceLabel is the label Docker Compose sets on the
// ownscale container itself (the "ownscale:" service in docker-compose.yml).
// Used the same way as caddyComposeServiceLabel, to find ownscale's own
// container ID by label instead of a name that varies by Compose project.
const ownscaleComposeServiceLabel = "com.docker.compose.service=ownscale"

// findContainerByLabel returns the ID of the running container matching
// label (a Docker filter of the form "key=value"), or "" if none is found.
// It's shared by findCaddyContainer and findOwnscaleContainer, which each
// look for a single, specific infra container by its Compose service label
// rather than a name that varies by Compose project.
func (c *Ctl) findContainerByLabel(ctx context.Context, label string) (_ string, err error) {
	defer derrors.Wrap(&err, "c.findContainerByLabel(%q)", label)

	containers, err := c.cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", label)),
	})
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return "", nil
	}
	return containers[0].ID, nil
}

// findCaddyContainer returns the ID of the running Caddy container (found
// via its Compose service label), or "" if none is found (e.g. Caddy isn't
// running, or this deployment doesn't use the bundled docker-compose.yml).
func (c *Ctl) findCaddyContainer(ctx context.Context) (_ string, err error) {
	defer derrors.Wrap(&err, "c.findCaddyContainer()")
	return c.findContainerByLabel(ctx, caddyComposeServiceLabel)
}

// findOwnscaleContainer returns the ID of the running ownscale container
// itself (found via its Compose service label), or "" if none is found
// (e.g. running outside the bundled docker-compose.yml, such as in tests
// or a bare `go run`).
func (c *Ctl) findOwnscaleContainer(ctx context.Context) (_ string, err error) {
	defer derrors.Wrap(&err, "c.findOwnscaleContainer()")
	return c.findContainerByLabel(ctx, ownscaleComposeServiceLabel)
}

// EnsureOwnscaleNetworkForOrg connects the ownscale container itself (found
// via its Compose service label) to subdomain's own per-organization
// network, if it isn't already connected. Unlike ensureCaddyNetworkAlias,
// no alias is needed: ownscale only ever needs to resolve the organization's
// headscale container by its plain Docker container name
// (HSContainerName(subdomain)), which Docker's embedded per-network DNS
// already provides to any container joined to that network.
//
// This is what lets the Go backend (headscaleclient) call
// http://hs-<subdomain>:8080 directly to manage that organization's nodes,
// the same way Caddy reaches it to proxy https://<subdomain>.<baseDomain>.
//
// It's idempotent, and exported so callers can reconcile this on ownscale
// process startup (per organization), not just when that organization is
// first created -- mirroring EnsureCaddyNetworkAliasForOrg. If the ownscale
// container isn't found (e.g. running outside docker-compose, as in most
// tests), this logs a warning and returns nil rather than failing
// organization creation: it only means the org's Machines tab can't reach
// headscale, not that the organization itself failed to provision.
func (c *Ctl) EnsureOwnscaleNetworkForOrg(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "c.EnsureOwnscaleNetworkForOrg(%q)", subdomain)

	netName, err := c.EnsureOrgNetwork(ctx, subdomain)
	if err != nil {
		return err
	}

	ownscaleID, err := c.findOwnscaleContainer(ctx)
	if err != nil {
		return err
	}
	if ownscaleID == "" {
		c.logger.Warn("ownscale container not found; skipping org network join (Machines tab won't reach headscale for this org)", "network", netName)
		return nil
	}

	inspected, err := c.cli.ContainerInspect(ctx, ownscaleID)
	if err != nil {
		return err
	}
	if inspected.NetworkSettings != nil {
		if _, connected := inspected.NetworkSettings.Networks[netName]; connected {
			return nil // already connected
		}
	}

	if err := c.cli.NetworkConnect(ctx, netName, ownscaleID, nil); err != nil {
		return fmt.Errorf("connect ownscale to %s: %w", netName, err)
	}
	return nil
}

// ensureCaddyNetworkAlias makes sure the Caddy container (found via its
// Compose service label) is connected to networkName with baseDomain as a
// network alias, so that any other container on networkName can resolve
// https://{baseDomain} straight to Caddy via Docker's embedded DNS, instead
// of failing DNS resolution (e.g. for BASE_DOMAIN=localhost) or relying on
// hairpinning back through the host's published ports.
//
// It's idempotent: a no-op if Caddy is already connected to networkName
// with the alias present. If Caddy isn't found at all, this logs a warning
// and returns nil rather than failing organization creation outright, since
// Caddy not running only breaks OIDC login for the new organization, not
// organization creation itself.
//
// This is deliberately written as a per-network operation (rather than
// hardcoded to the shared ownscaleNetwork): EnsureCaddyNetworkAlias calls it
// for ownscaleNetwork itself, while EnsureCaddyNetworkAliasForOrg calls it
// for each organization's own isolated network.
func (c *Ctl) ensureCaddyNetworkAlias(ctx context.Context, networkName, baseDomain string) (err error) {
	defer derrors.Wrap(&err, "c.ensureCaddyNetworkAlias(%q, %q)", networkName, baseDomain)

	caddyID, err := c.findCaddyContainer(ctx)
	if err != nil {
		return err
	}
	if caddyID == "" {
		c.logger.Warn("caddy container not found; skipping network alias setup (OIDC discovery from org containers may fail)", "network", networkName, "base_domain", baseDomain)
		return nil
	}

	inspected, err := c.cli.ContainerInspect(ctx, caddyID)
	if err != nil {
		return err
	}

	var existing *network.EndpointSettings
	if inspected.NetworkSettings != nil {
		existing = inspected.NetworkSettings.Networks[networkName]
	}
	if existing != nil && slices.Contains(existing.Aliases, baseDomain) {
		return nil // already aliased
	}

	aliases := []string{baseDomain}
	if existing != nil {
		aliases = append(aliases, existing.Aliases...)
		// Reconnecting requires disconnecting first if already attached.
		if err := c.cli.NetworkDisconnect(ctx, networkName, caddyID, false); err != nil {
			return fmt.Errorf("disconnect caddy from %s: %w", networkName, err)
		}
	}
	if err := c.cli.NetworkConnect(ctx, networkName, caddyID, &network.EndpointSettings{Aliases: aliases}); err != nil {
		return fmt.Errorf("connect caddy to %s with alias %q: %w", networkName, baseDomain, err)
	}
	return nil
}

// headscale config for an organization; ServerURL must match the public subdomain.
// The "derp" block is filled in by buildHSConfig depending on whether a
// custom DERP relay (config.DerpConfig) is configured: either today's default
// (Tailscale's public DERP map, no embedded server), or a custom derp.paths
// entry pointing at the rendered derp.yaml (see configTemplateDerp), with
// the public map disabled.
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
{{- if .HasCustomDerp}}
  paths:
    - {{.DerpConfigPath}}
  urls: []
{{- else}}
  urls:
    - https://controlplane.tailscale.com/derpmap/default
{{- end}}
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

taildrop:
  enabled: true

dns:
  magic_dns: true
  base_domain: {{.Subdomain}}.headscale.internal
  nameservers:
    global:
      - 1.1.1.1

unix_socket: /var/run/headscale/headscale.sock
unix_socket_permission: "0770"

policy:
  mode: file
  path: {{.PolicyConfigPath}}

oidc:
  issuer: "{{.OIDCIssuer}}"
  client_id: "{{.OIDCClientID}}"
  client_secret: "{{.OIDCClientSecret}}"
  scope: ["openid", "profile", "email", "offline_access"]
  pkce:
    enabled: true
    method: S256
`))

// DefaultACLPolicy is the HuJSON access control policy every organization
// starts with: a single accept-all rule allowing every node to reach every
// other node on every port. This deliberately matches headscale's
// no-policy-configured behavior (unrestricted tailnet access) so enabling
// the policy file doesn't change behavior for existing/new organizations
// until an admin edits it from the Access Control page.
const DefaultACLPolicy = `{
    // Lines starting with // are comments.
    // Leave comments for future reference or explanations about the settings.
	// See https://headscale.net/stable/ref/acls/ for the full HuJSON syntax.
	"acls": [
		{
			"action": "accept",
			"src": ["*"],
			"dst": ["*:*"],
		},
	],
}
`

// custom DERP map for an organization, in headscale's own format (see
// https://github.com/juanfont/headscale/blob/main/derp-example.yaml),
// describing the single self-hosted relay from deploy/derper.
var configTemplateDerp = template.Must(template.New("derpConfig").Parse(`regions:
  {{.RegionID}}:
    regionid: {{.RegionID}}
    regioncode: {{.RegionCode}}
    regionname: {{.RegionName}}
    nodes:
      - name: {{.RegionCode}}a
        regionid: {{.RegionID}}
        hostname: {{.Hostname}}
        {{- if .IPv4}}
        ipv4: {{.IPv4}}
        {{- end}}
        {{- if .IPv6}}
        ipv6: {{.IPv6}}
        {{- end}}
        stunport: {{.STUNPort}}
        stunonly: false
        derpport: 0
`))

// buildDerpConfig renders the custom derp.yaml content for an organization
// from derpCfg. Only called when derpCfg.Enabled().
func buildDerpConfig(derpCfg config.DerpConfig) (_ string, err error) {
	defer derrors.Wrap(&err, "buildDerpConfig(%q)", derpCfg.Hostname)

	var buf bytes.Buffer
	if err := configTemplateDerp.Execute(&buf, derpCfg); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// buildHSConfig renders the headscale config.yaml content for an organization.
func buildHSConfig(subdomain, domain string, oidcCreds OIDCClientCredentials, derpCfg config.DerpConfig) (_ string, err error) {
	defer derrors.Wrap(&err, "buildHSConfig(%q, %q)", subdomain, domain)

	var buf bytes.Buffer
	if err := configTemplateHS.Execute(&buf, struct {
		Subdomain        string
		ServerURL        string
		OIDCIssuer       string
		OIDCClientID     string
		OIDCClientSecret string
		HasCustomDerp    bool
		DerpConfigPath   string
		PolicyConfigPath string
	}{
		Subdomain:        subdomain,
		ServerURL:        domain,
		OIDCIssuer:       oidcCreds.Issuer,
		OIDCClientID:     oidcCreds.ClientID,
		OIDCClientSecret: oidcCreds.ClientSecret,
		HasCustomDerp:    derpCfg.Enabled(),
		DerpConfigPath:   derpConfigPath,
		PolicyConfigPath: PolicyConfigPath,
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// headplane config for an organization; BaseURL must be the public
// subdomain URL (without the /admin dashboard prefix), and headscale.url
// points at the organization's own headscale container over the shared
// Docker network. Docker integration is enabled to power Headplane's
// Network Management feature (editing DNS/MagicDNS/ACL settings for the
// Tailnet from the UI). CAUTION: this docker integrations relies on sharing the
// docker socket.
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
  config_path: "{{.HSConfigPath}}"
  api_key: "{{.HeadscaleAPIKey}}"

oidc:
  enabled: {{if .OIDCIssuer}}true{{else}}false{{end}}
  issuer: "{{if .OIDCIssuer}}{{.OIDCIssuer}}{{else}}http://disabled.invalid{{end}}"
  token_endpoint_auth_method: "client_secret_basic"
  client_id: "{{.OIDCClientID}}"
  client_secret: "{{.OIDCClientSecret}}"
  use_pkce: true
  disable_api_key_login: false
  scope: "openid email profile offline_access"

integration:
  docker:
    enabled: true
    container_name: "{{.HSContainerName}}"
    socket: "unix:///var/run/docker.sock"
`))

// buildHPConfig renders the headplane config.yaml content for an
// organization.
func buildHPConfig(subdomain, domain, cookieSecret, headscaleAPIKey string, oidcCreds OIDCClientCredentials) (_ string, err error) {
	defer derrors.Wrap(&err, "buildHPConfig(%q, %q, %q)", subdomain, domain, cookieSecret)

	var buf bytes.Buffer
	type tplData struct {
		BaseURL            string
		CookieSecret       string
		HSContainerName    string
		HSExternalEndpoint string
		HSConfigPath       string
		HeadscaleAPIKey    string
		OIDCIssuer         string
		OIDCClientID       string
		OIDCClientSecret   string
	}
	if err := configTemplateHP.Execute(&buf, tplData{
		BaseURL:            domain,
		CookieSecret:       cookieSecret,
		HSContainerName:    HSContainerName(subdomain),
		HSExternalEndpoint: HSExternalEndpoint(domain),
		HSConfigPath:       headscaleConfigPath,
		HeadscaleAPIKey:    headscaleAPIKey,
		OIDCIssuer:         oidcCreds.Issuer,
		OIDCClientID:       oidcCreds.ClientID,
		OIDCClientSecret:   oidcCreds.ClientSecret,
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
func configTar(path, content string) (_ *bytes.Buffer, err error) {
	defer derrors.Wrap(&err, "configTar(%s)", path)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	rel := strings.TrimPrefix(path, "/")
	var dir strings.Builder
	for part := range strings.SplitSeq(filepath.Dir(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		dir.WriteString(part + "/")
		if err := tw.WriteHeader(&tar.Header{
			Name:     dir.String(),
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

// MintHeadscaleAPIKey execs `headscale apikeys create` inside the headscale
// container identified by containerNameOrID (Docker's exec-create endpoint
// accepts either a container name or its ID, so this works both right after
// CreateOrganization creates a container -- passing its fresh ID, before it
// necessarily has a name-based DNS entry anywhere relevant -- and later,
// against an already-running organization, by passing HSContainerName(subdomain)).
// Every call mints a brand new, independent, long-lived (10 year) API key;
// headscale supports any number of valid keys at once, so minting another
// one doesn't invalidate keys already handed out (e.g. to headplane).
func (c *Ctl) MintHeadscaleAPIKey(ctx context.Context, containerNameOrID string) (_ string, err error) {
	defer derrors.Wrap(&err, "c.MintHeadscaleAPIKey(%q)", containerNameOrID)

	var lastErr error
	for attempt := range 5 {
		// Tty is deliberately false: with Tty:true some Docker Engine
		// versions still frame the attach stream with stdcopy's
		// multiplexing headers (rather than raw bytes), which corrupts the
		// API key. Tty:false makes stdcopy demuxing mandatory instead of
		// version-dependent, so we always decode it explicitly.
		execResp, err := c.cli.ContainerExecCreate(ctx, containerNameOrID, container.ExecOptions{
			AttachStdout: true,
			AttachStderr: true,
			Cmd:          []string{"headscale", "apikeys", "create", "--expiration", "87600h"},
		})
		if err != nil {
			lastErr = err
		} else {
			attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
			if err != nil {
				lastErr = err
			} else {
				var stdout, stderr bytes.Buffer
				_, readErr := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
				attachResp.Close()
				out := stdout.Bytes()
				if readErr != nil {
					lastErr = readErr
				} else {
					inspectResp, inspectErr := c.cli.ContainerExecInspect(ctx, execResp.ID)
					if inspectErr != nil {
						lastErr = inspectErr
					} else if inspectResp.ExitCode == 0 {
						apiKey := strings.TrimSpace(string(out))
						if apiKey != "" {
							return apiKey, nil
						}
						lastErr = fmt.Errorf("empty API key output")
					} else {
						lastErr = fmt.Errorf("exit code %d: stdout=%q stderr=%q", inspectResp.ExitCode, strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()))
					}
				}
			}
		}
		if attempt < 4 {
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
		}
	}
	return "", lastErr
}

// MintClient implements headscaleclient.ClientMinter: it mints a fresh API
// key for subdomain's headscale container and returns a Client pointed at
// http://hs-<subdomain>:8080, reachable because the ownscale container is
// joined to subdomain's Docker network (see EnsureOwnscaleNetworkForOrg).
func (c *Ctl) MintClient(ctx context.Context, subdomain string) (_ *headscaleclient.Client, err error) {
	defer derrors.Wrap(&err, "c.MintClient(%q)", subdomain)

	containerName := HSContainerName(subdomain)
	apiKey, err := c.MintHeadscaleAPIKey(ctx, containerName)
	if err != nil {
		return nil, err
	}
	return headscaleclient.New(containerName+":8080", apiKey), nil
}

// waitForHTTPServing execs into containerID repeatedly, using Node's own
// HTTP client (Node is guaranteed present since this is used to probe
// headplane, itself a Node app) to check whether something is actually
// listening and responding at url. It retries with a fixed backoff for up
// to ~10s and returns an error if nothing ever responds.
//
// A container reaching "started" doesn't mean the process inside has
// finished booting and bound its listening socket yet -- for a Node app in
// particular there can be a noticeable gap between "docker start" and the
// server accepting connections. Without this, an organization could be
// marked "active" (see orgs.Service.Provision) while headplane is still
// starting up, so a user redirected there immediately after provisioning
// would hit a connection error instead of the login page.
func (c *Ctl) waitForHTTPServing(ctx context.Context, containerID, url string) (err error) {
	defer derrors.Wrap(&err, "c.waitForHTTPServing(%q, %q)", containerID, url)

	// Exit 0 if we get any HTTP response at all (status doesn't matter --
	// we only care whether something is listening), exit 1 on any
	// connection-level error (not listening yet, connection reset, ...).
	probe := fmt.Sprintf(
		"require('http').get(%q, res => { res.resume(); process.exit(0); }).on('error', () => process.exit(1));",
		url,
	)

	const attempts = 20
	const backoff = 500 * time.Millisecond

	var lastErr error
	for attempt := range attempts {
		if err := ctx.Err(); err != nil {
			return err
		}

		execResp, err := c.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
			AttachStdout: true,
			AttachStderr: true,
			Cmd:          []string{"node", "-e", probe},
		})
		if err != nil {
			lastErr = err
		} else if attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{}); err != nil {
			lastErr = err
		} else {
			var stdout, stderr bytes.Buffer
			_, readErr := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
			attachResp.Close()
			if readErr != nil {
				lastErr = readErr
			} else if inspectResp, inspectErr := c.cli.ContainerExecInspect(ctx, execResp.ID); inspectErr != nil {
				lastErr = inspectErr
			} else if inspectResp.ExitCode == 0 {
				return nil
			} else {
				lastErr = fmt.Errorf("not serving yet: %s", strings.TrimSpace(stderr.String()))
			}
		}

		if attempt < attempts-1 {
			time.Sleep(backoff)
		}
	}
	return lastErr
}

// CreateOrganization provisions and starts a headscale container serving
// https://<subdomain>.<baseDomain>. aclPolicy is the HuJSON access control
// policy content to upload at policyConfigPath (see DefaultACLPolicy for
// the value new organizations start with). It returns the container ID.
func (c *Ctl) CreateOrganization(ctx context.Context, subdomain, baseDomain string, oidcCreds OIDCClientCredentials, derpCfg config.DerpConfig, aclPolicy string) (_ string, err error) {
	defer derrors.Wrap(&err, "c.CreateOrganization(%q, %q)", subdomain, baseDomain)

	if err := ValidateSubdomain(subdomain); err != nil {
		return "", err
	}
	if err := c.EnsureCaddyNetworkAliasForOrg(ctx, subdomain, baseDomain); err != nil {
		return "", err
	}
	if err := c.EnsureOwnscaleNetworkForOrg(ctx, subdomain); err != nil {
		return "", err
	}
	orgNetwork := OrgNetworkName(subdomain)

	domain := subdomain + "." + baseDomain
	cfg, err := buildHSConfig(subdomain, domain, oidcCreds, derpCfg)
	if err != nil {
		return "", err
	}

	hostDir := c.headscaleHostDir(subdomain)
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}

	configHostDir := c.headscaleConfigHostDir(subdomain)
	if err := os.MkdirAll(configHostDir, 0o755); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	configHostFile := filepath.Join(configHostDir, "config.yaml")
	if err := os.WriteFile(configHostFile, []byte(cfg), 0o644); err != nil {
		return "", fmt.Errorf("write config file: %w", err)
	}

	mounts := []mount.Mount{
		{Type: mount.TypeBind, Source: hostDir, Target: "/var/lib/headscale"},
		// Read-only: headscale only ever reads its own config; Headplane
		// (given write access below) is what edits it.
		{Type: mount.TypeBind, Source: configHostFile, Target: headscaleConfigPath, ReadOnly: true},
	}
	var env []string
	if m, ok := c.localCAMount(); ok {
		mounts = append(mounts, m)
		env = append(env, "SSL_CERT_DIR=/etc/ssl/certs:/etc/pki/tls/certs:"+LocalCAMountTarget)
	}

	resp, err := c.cli.ContainerCreate(ctx,
		&container.Config{
			Image: Image,
			Cmd:   []string{"serve"},
			Env:   env,
			Labels: map[string]string{
				"ownscale.organization": "true",
				"ownscale.subdomain":    subdomain,
				"ownscale.domain":       domain,
			},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Mounts:        mounts,
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				orgNetwork: {},
			},
		},
		nil,
		HSContainerName(subdomain),
	)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	if derpCfg.Enabled() {
		derpYAML, err := buildDerpConfig(derpCfg)
		if err != nil {
			return "", err
		}
		derpTarBuf, err := configTar(derpConfigPath, derpYAML)
		if err != nil {
			return "", err
		}
		if err := c.cli.CopyToContainer(ctx, resp.ID, "/", derpTarBuf, container.CopyToContainerOptions{}); err != nil {
			return "", fmt.Errorf("copy derp config into container: %w", err)
		}
	}

	// aclPolicy is uploaded straight into the container's filesystem via
	// the Docker API (not bind-mounted): the only way it ever changes is
	// another such upload from ownscale itself (see WriteACLPolicy),
	// keeping the file's source of truth firmly in the orgconfigs table.
	policyTarBuf, err := configTar(PolicyConfigPath, aclPolicy)
	if err != nil {
		return "", err
	}
	if err := c.cli.CopyToContainer(ctx, resp.ID, "/", policyTarBuf, container.CopyToContainerOptions{}); err != nil {
		return "", fmt.Errorf("copy acl policy into container: %w", err)
	}

	if err := c.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}
	go c.streamLogs(resp.ID, HSContainerName(subdomain))

	headscaleAPIKey, err := c.MintHeadscaleAPIKey(ctx, resp.ID)
	if err != nil {
		return "", err
	}

	if err := c.createHeadplane(ctx, subdomain, domain, headscaleAPIKey, oidcCreds); err != nil {
		return "", err
	}

	return resp.ID, nil
}

// createHeadplane provisions and starts the headplane container for an
// organization, configured to talk to that organization's headscale
// container over the organization's own isolated Docker network.
func (c *Ctl) createHeadplane(ctx context.Context, subdomain, domain, headscaleAPIKey string, oidcCreds OIDCClientCredentials) (err error) {
	defer derrors.Wrap(&err, "c.createHeadplane(%q, %q)", subdomain, domain)

	cookieSecret, err := randomCookieSecret()
	if err != nil {
		return fmt.Errorf("generate cookie secret: %w", err)
	}
	cfg, err := buildHPConfig(subdomain, domain, cookieSecret, headscaleAPIKey, oidcCreds)
	if err != nil {
		return err
	}

	hostDir := c.headplaneHostDir(subdomain)
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	// Written by CreateOrganization before this is called.
	configHostFile := filepath.Join(c.headscaleConfigHostDir(subdomain), "config.yaml")

	mounts := []mount.Mount{
		{Type: mount.TypeBind, Source: hostDir, Target: "/var/lib/headplane"},
		{Type: mount.TypeBind, Source: configHostFile, Target: headscaleConfigPath},
		{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: true},
	}
	if m, ok := c.localCAMount(); ok {
		mounts = append(mounts, m)
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
			// Node.js only reads NODE_EXTRA_CA_CERTS in addition to its
			// built-in trust store (it doesn't replace it), and silently
			// ignores the variable if the file doesn't exist -- so this is
			// safe to always set: it makes headplane trust the local
			// development root CA.
			Env: []string{"NODE_EXTRA_CA_CERTS=" + filepath.Join(LocalCAMountTarget, localca.RootCertFileName)},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Mounts:        mounts,
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				OrgNetworkName(subdomain): {},
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
		return err
	}
	if err := c.cli.CopyToContainer(ctx, resp.ID, "/", tarBuf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copy config into container: %w", err)
	}

	if err := c.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	go c.streamLogs(resp.ID, HPContainerName(subdomain))

	// Headplane only serves at /admin.
	if err := c.waitForHTTPServing(ctx, resp.ID, "http://localhost:3000/admin/"); err != nil {
		return err
	}
	return nil
}

// RemoveOrganization stops and removes the headscale and headplane
// containers and their data (the headscale, headplane and headscale-config
// host directories under DataDir), and the organization's own isolated
// Docker network (disconnecting Caddy from it first). Missing resources are
// ignored so
// removal is idempotent.
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
	if err := os.RemoveAll(c.headscaleConfigHostDir(subdomain)); err != nil {
		return fmt.Errorf("remove config directory: %w", err)
	}

	hpName := HPContainerName(subdomain)
	if err := c.cli.ContainerRemove(ctx, hpName, container.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove headplane container: %w", err)
	}
	if err := os.RemoveAll(c.headplaneHostDir(subdomain)); err != nil {
		return fmt.Errorf("remove headplane data directory: %w", err)
	}

	c.removeOrgNetwork(ctx, subdomain)
	return nil
}

// removeOrgNetwork disconnects Caddy and the ownscale container (if
// connected) from subdomain's per-organization network and removes the
// network itself. It's deliberately best-effort (failures are logged, not
// returned): by the time it's called, RemoveOrganization has already
// deleted the organization's containers and data, so a leftover network
// (or a failed disconnect, e.g. because Caddy/ownscale isn't running) is
// harmless clutter rather than a correctness problem, and shouldn't fail
// organization removal.
func (c *Ctl) removeOrgNetwork(ctx context.Context, subdomain string) {
	netName := OrgNetworkName(subdomain)

	caddyID, err := c.findCaddyContainer(ctx)
	if err != nil {
		c.logger.Warn("find caddy container for org network cleanup", "network", netName, "error", err)
	} else if caddyID != "" {
		if err := c.cli.NetworkDisconnect(ctx, netName, caddyID, true); err != nil && !cerrdefs.IsNotFound(err) {
			c.logger.Warn("disconnect caddy from org network", "network", netName, "error", err)
		}
	}

	ownscaleID, err := c.findOwnscaleContainer(ctx)
	if err != nil {
		c.logger.Warn("find ownscale container for org network cleanup", "network", netName, "error", err)
	} else if ownscaleID != "" {
		if err := c.cli.NetworkDisconnect(ctx, netName, ownscaleID, true); err != nil && !cerrdefs.IsNotFound(err) {
			c.logger.Warn("disconnect ownscale from org network", "network", netName, "error", err)
		}
	}

	if err := c.cli.NetworkRemove(ctx, netName); err != nil && !cerrdefs.IsNotFound(err) {
		c.logger.Warn("remove org network", "network", netName, "error", err)
	}
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

// WriteACLPolicy uploads content (a HuJSON access control policy) into
// subdomain's running headscale container at policyConfigPath via the
// Docker API's CopyToContainer, overwriting whatever policy file is
// already there. It does not restart the container -- call
// RestartHeadscale afterwards so headscale actually reloads the new
// policy (headscale only reads its policy file at startup).
func (c *Ctl) WriteACLPolicy(ctx context.Context, subdomain, content string) (err error) {
	defer derrors.Wrap(&err, "c.WriteACLPolicy(%q)", subdomain)

	tarBuf, err := configTar(PolicyConfigPath, content)
	if err != nil {
		return err
	}
	if err := c.cli.CopyToContainer(ctx, HSContainerName(subdomain), "/", tarBuf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copy acl policy into container: %w", err)
	}
	return nil
}

// RestartHeadscale restarts subdomain's headscale container via the Docker
// API, so it picks up a newly uploaded ACL policy (or any other on-disk
// config change). Headplane, running on its own container, is unaffected.
func (c *Ctl) RestartHeadscale(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "c.RestartHeadscale(%q)", subdomain)

	if err := c.cli.ContainerRestart(ctx, HSContainerName(subdomain), container.StopOptions{}); err != nil {
		return fmt.Errorf("restart container: %w", err)
	}
	return nil
}

// SetOIDCAllowlist updates the OIDC allowlist in config.yaml and restarts headscale
// for reload. TODO: when we drop headplane, this function will not need to mount
// the config anymore. Instead it will be possible to generate it and use docker
// API to upload it to container.
func (c *Ctl) SetOIDCAllowlist(ctx context.Context, subdomain string, domains, groups, users []string) (err error) {
	defer derrors.Wrap(&err, "c.SetOIDCAllowlist(%q)", subdomain)

	configHostFile := filepath.Join(c.headscaleConfigHostDir(subdomain), "config.yaml")
	raw, err := os.ReadFile(configHostFile)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse config file: %w", err)
	}
	oidc, _ := doc["oidc"].(map[string]any)
	if oidc == nil {
		oidc = make(map[string]any)
	}
	oidc["allowed_domains"] = domains
	oidc["allowed_groups"] = groups
	oidc["allowed_users"] = users
	doc["oidc"] = oidc

	updated, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal config file: %w", err)
	}
	if err := os.WriteFile(configHostFile, updated, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	if err := c.RestartHeadscale(ctx, subdomain); err != nil {
		return err
	}
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
// because they're reserved for fixed, non-organization Caddy site blocks:
// the ownscale app itself (Ownscale console, login, OIDC provider) is bound to
// the bare base domain, "docs" serves the documentation site, and "admin"/
// "auth" are kept reserved defensively even though they aren't currently
// bound to their own Caddy site blocks, to avoid confusion with the app's
// former routing and any future re-introduction of dedicated subdomains.
var reservedSubdomains = map[string]bool{
	"admin": true,
	"auth":  true,
	"docs":  true,
	"www":   true,
	"derp":  true,
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
