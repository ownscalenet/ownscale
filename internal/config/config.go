// Package config loads the admin panel's runtime configuration, following a
// pattern similar to github.com/juanfont/headscale: a YAML config file
// (either the one given via --config, or /etc/ownscale/config.yaml by
// default) is read on top of built-in defaults, and OWNSCALE_*-prefixed
// environment variables can override any value from either source.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/internal/derrors"

	"github.com/caarlos0/env/v11"
	"gopkg.in/yaml.v3"
)

// DefaultPath is where the config file is looked for when --config is not given.
const DefaultPath = "config.yaml"

// Config is the admin panel's runtime configuration.
type Config struct {
	// Addr is the address the HTTP server listens on.
	Addr string `yaml:"addr" env:"OWNSCALE_ADDR"`

	// BaseDomain is the wildcard domain organizations are provisioned under
	// (e.g. "example.com" for "<sub>.example.com"). Required to run the
	// server and to use the organization CLI subcommands.
	BaseDomain string `yaml:"base_domain" env:"OWNSCALE_BASE_DOMAIN"`

	// DataDir is the directory under which all SQLite-backed state lives:
	// this process's own database (see Database) plus a
	// "headscale/<subdomain>" subdirectory bind-mounted into each
	// organization's headscale container (see internal/dockerctl).
	//
	// It must resolve to a real, absolute path on the machine running the
	// Docker daemon: dockerctl hands (a subdirectory of) this exact path to
	// the Docker API as a bind-mount source when creating each
	// organization's headscale container, and that's resolved by the
	// daemon, not by this process's own filesystem view. That's normally
	// only a concern in the docker-compose deployment, where this process
	// itself runs in a container talking to the *host's* Docker daemon over
	// a mounted socket (the same "Docker-outside-of-Docker" situation the
	// socket mount itself is already in) — see docker-compose.yml, which
	// bind-mounts OWNSCALE_DATA_DIR at an identical path on both sides so
	// this process and the host daemon agree on the same real path. In
	// that setup DataDir/OWNSCALE_DATA_DIR must be set explicitly. When
	// this process runs natively (not containerized), the default below is
	// resolved via filepath.Abs against the real host filesystem, so it
	// works out of the box.
	DataDir string `yaml:"data_dir" env:"OWNSCALE_DATA_DIR"`

	// CaddyLocalCerts mirrors the CADDY_LOCAL_CERTS value passed to the
	// caddy service in docker-compose.yml (see etc/caddy/Caddyfile). It
	// should be non-empty (typically "local_certs") whenever Caddy is
	// configured to issue its own internal, self-signed certificates
	// instead of ACME/Let's Encrypt-issued ones (i.e. BASE_DOMAIN isn't a
	// real, publicly resolvable domain). When non-empty, dockerctl copies
	// Caddy's local CA into each new headscale container and treats that
	// copy as mandatory, failing organization creation outright if it's
	// missing, instead of silently creating a container that can't
	// complete OIDC login later. When empty (production, real ACME cert),
	// dockerctl skips that step entirely.
	CaddyLocalCerts string `yaml:"caddy_local_certs" env:"OWNSCALE_CADDY_LOCAL_CERTS"`

	Auth AuthConfig `yaml:"auth"`
	Log  LogConfig  `yaml:"log"`
}

// LogConfig configures the application's structured (slog) logger.
type LogConfig struct {
	Level string `yaml:"level" env:"OWNSCALE_LOG_LEVEL"`
	// Format can be "json" or "text".
	Format string `yaml:"format" env:"OWNSCALE_LOG_FORMAT"`
}

// AuthConfig configures user authentication.
type AuthConfig struct {
	// SessionSecret signs the login session cookie. Required to run the server.
	SessionSecret string `yaml:"session_secret" env:"OWNSCALE_AUTH_SESSION_SECRET"`

	// UpstreamOIDC configures the upstream OpenID Connect provider ownscale
	// federates the initial admin panel login to (e.g. Google in
	// production, or a local Dex instance for development). Not to be
	// confused with OIDCProvider below: that's ownscale acting as its own
	// (downstream) OIDC provider for organization containers, whereas this
	// is ownscale acting as an OIDC *client* of some other, upstream IdP.
	UpstreamOIDC UpstreamOIDCConfig `yaml:"upstream_oidc"`

	// OIDCProvider configures ownscale's own OIDC provider, which
	// downstream per-organization headscale/headplane containers trust
	// (federated to UpstreamOIDC above for the initial login). See
	// internal/auth/oidcprovider.
	OIDCProvider OIDCProviderConfig `yaml:"oidc_provider"`
}

// UpstreamOIDCConfig configures the upstream OpenID Connect provider used
// for the admin panel's own login (e.g. Google, or a local Dex instance for
// development). ownscale acts as a standard OIDC relying party against it.
type UpstreamOIDCConfig struct {
	// Issuer is the upstream provider's issuer URL, used for OIDC discovery
	// (".well-known/openid-configuration"), e.g. "https://accounts.google.com"
	// or "https://dex.<base_domain>" for the bundled local-dev Dex instance.
	// Required to run the server.
	Issuer string `yaml:"issuer" env:"OWNSCALE_AUTH_UPSTREAM_OIDC_ISSUER"`
	// ClientID is the OAuth 2.0 client ID registered with Issuer. Required
	// to run the server.
	ClientID string `yaml:"client_id" env:"OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_ID"`
	// ClientSecret is the OAuth 2.0 client secret registered with Issuer.
	// Required to run the server.
	ClientSecret string `yaml:"client_secret" env:"OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_SECRET"`
	// CallbackURL must match the redirect URI registered with Issuer (e.g.
	// "https://<base_domain>/auth/oidc/callback"). Required to run the
	// server.
	CallbackURL string `yaml:"callback_url" env:"OWNSCALE_AUTH_UPSTREAM_OIDC_CALLBACK_URL"`
}

// OIDCProviderConfig configures ownscale acting as an OpenID Connect
// provider for downstream relying parties (each organization's headscale
// and headplane containers). See internal/auth/oidcprovider.
type OIDCProviderConfig struct {
	// Issuer is the public base URL clients (and relying parties) use to
	// reach ownscale's OIDC endpoints (e.g. "/.well-known/openid-configuration",
	// "/oauth/authorize"). Must exactly match what's advertised in
	// discovery and in issued tokens. Defaults to "https://<base_domain>"
	// when unset (ownscale itself, i.e. the admin panel/login origin, is
	// bound to the bare base domain by etc/caddy/Caddyfile).
	Issuer string `yaml:"issuer" env:"OWNSCALE_AUTH_OIDC_PROVIDER_ISSUER"`

	// SigningKeyPath is where the RSA private key used to sign issued ID
	// tokens is stored, relative to DataDir unless absolute. Generated
	// automatically on first startup if the file doesn't exist yet.
	// Defaults to "oidc_signing_key.pem" (under DataDir).
	SigningKeyPath string `yaml:"signing_key_path" env:"OWNSCALE_AUTH_OIDC_PROVIDER_SIGNING_KEY_PATH"`

	// AccessTokenLifetime is how long issued OAuth access tokens are
	// valid for, expressed as a Go duration string (e.g. "5m"). Defaults
	// to "5m".
	AccessTokenLifetime string `yaml:"access_token_lifetime" env:"OWNSCALE_AUTH_OIDC_PROVIDER_ACCESS_TOKEN_LIFETIME"`

	// IDTokenLifetime is how long issued OIDC ID tokens are valid for,
	// expressed as a Go duration string (e.g. "1h"). Defaults to "1h".
	IDTokenLifetime string `yaml:"id_token_lifetime" env:"OWNSCALE_AUTH_OIDC_PROVIDER_ID_TOKEN_LIFETIME"`
}

func defaults() *Config {
	return &Config{
		Addr:    ":8080",
		DataDir: "data",
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
		Auth: AuthConfig{
			OIDCProvider: OIDCProviderConfig{
				SigningKeyPath:      "oidc_signing_key.pem",
				AccessTokenLifetime: "5m",
				IDTokenLifetime:     "1h",
			},
		},
	}
}

// Load builds the Config from, in increasing order of precedence: built-in
// defaults, a YAML config file, and OWNSCALE_*-prefixed environment
// variables.
//
// If path is non-empty (i.e. --config was given explicitly), that exact file
// must exist and parse cleanly, or Load returns an error. If path is empty,
// Load looks for DefaultPath; if it is not there, Load silently proceeds
// with defaults and environment variables only (mirroring headscale's
// "no config file found, using defaults" behavior).
func Load(path string) (_ *Config, err error) {
	defer derrors.Wrap(&err, "config.Load(%q)", path)

	cfg := defaults()

	explicit := path != ""
	if path == "" {
		path = DefaultPath
	}

	f, err := os.Open(path)
	switch {
	case err == nil:
		dec := yaml.NewDecoder(f)
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("parse config file %s: %w", path, err)
		}
	case explicit:
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	default:
		fmt.Fprintf(os.Stderr, "no config file found at %s, using defaults and environment variables\n", path)
	}

	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parse environment variables: %w", err)
	}

	if cfg.DataDir != "" {
		abs, err := filepath.Abs(cfg.DataDir)
		if err != nil {
			return nil, fmt.Errorf("resolve data_dir %q: %w", cfg.DataDir, err)
		}
		cfg.DataDir = abs
	}

	// The OIDC provider issuer defaults to "https://<base_domain>" (ownscale
	// itself is bound to the bare base domain by etc/caddy/Caddyfile), which
	// can only be computed once base_domain is known (i.e. after YAML/env
	// have been applied).
	if cfg.Auth.OIDCProvider.Issuer == "" && cfg.BaseDomain != "" {
		cfg.Auth.OIDCProvider.Issuer = "https://" + cfg.BaseDomain
	}
	// SigningKeyPath is stored under DataDir unless already absolute.
	if cfg.Auth.OIDCProvider.SigningKeyPath != "" && cfg.DataDir != "" && !filepath.IsAbs(cfg.Auth.OIDCProvider.SigningKeyPath) {
		cfg.Auth.OIDCProvider.SigningKeyPath = filepath.Join(cfg.DataDir, cfg.Auth.OIDCProvider.SigningKeyPath)
	}

	return cfg, nil
}

// Validate checks that the fields required to run the HTTP server are set.
func (c *Config) Validate() (err error) {
	defer derrors.Wrap(&err, "config.(*Config).Validate()")

	var missing []string
	if c.BaseDomain == "" {
		missing = append(missing, "base_domain")
	}
	if c.DataDir == "" {
		missing = append(missing, "data_dir")
	}
	if c.Auth.SessionSecret == "" {
		missing = append(missing, "auth.session_secret")
	}
	if c.Auth.UpstreamOIDC.Issuer == "" {
		missing = append(missing, "auth.upstream_oidc.issuer")
	}
	if c.Auth.UpstreamOIDC.ClientID == "" {
		missing = append(missing, "auth.upstream_oidc.client_id")
	}
	if c.Auth.UpstreamOIDC.ClientSecret == "" {
		missing = append(missing, "auth.upstream_oidc.client_secret")
	}
	if c.Auth.UpstreamOIDC.CallbackURL == "" {
		missing = append(missing, "auth.upstream_oidc.callback_url")
	}
	if c.Auth.OIDCProvider.Issuer == "" {
		missing = append(missing, "auth.oidc_provider.issuer")
	}
	if c.Auth.OIDCProvider.SigningKeyPath == "" {
		missing = append(missing, "auth.oidc_provider.signing_key_path")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config values: %s", strings.Join(missing, ", "))
	}
	if strings.EqualFold(c.BaseDomain, "localhost") {
		return fmt.Errorf(`base_domain must not be "localhost": it's a special-cased name that every container's ` +
			`(and most OSes') resolver answers from a built-in loopback entry before ever consulting DNS, which ` +
			`breaks Docker-network-alias-based routing between organization containers and Caddy. Use a dedicated ` +
			`local dev domain instead (e.g. "ownscale.test") with a wildcard entry in your host's /etc/hosts, e.g.: ` +
			`127.0.0.1 ownscale.test *.ownscale.test`)
	}
	if c.DataDir != "" && !filepath.IsAbs(c.DataDir) {
		return fmt.Errorf("data_dir must resolve to an absolute path, got %q", c.DataDir)
	}
	if _, err := time.ParseDuration(c.Auth.OIDCProvider.AccessTokenLifetime); err != nil {
		return fmt.Errorf("auth.oidc_provider.access_token_lifetime: %w", err)
	}
	if _, err := time.ParseDuration(c.Auth.OIDCProvider.IDTokenLifetime); err != nil {
		return fmt.Errorf("auth.oidc_provider.id_token_lifetime: %w", err)
	}
	return nil
}

func (c Config) Database() string {
	return filepath.Join(c.DataDir, "ownscale.sqlite")
}
