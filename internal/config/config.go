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
// See config.example.yaml for documentation about these fields.
type Config struct {
	Addr       string `yaml:"addr" env:"OWNSCALE_ADDR"`
	BaseDomain string `yaml:"base_domain" env:"OWNSCALE_BASE_DOMAIN"`
	DataDir    string `yaml:"data_dir" env:"OWNSCALE_DATA_DIR"`
	LocalCADir string `yaml:"local_ca_dir" env:"OWNSCALE_LOCAL_CA_DIR"`

	Auth AuthConfig `yaml:"auth"`
	Log  LogConfig  `yaml:"log"`
	Derp DerpConfig `yaml:"derp"`
}

// LogConfig configures the application's structured (slog) logger.
type LogConfig struct {
	Level string `yaml:"level" env:"OWNSCALE_LOG_LEVEL"`
	// Format can be "json" or "text".
	Format string `yaml:"format" env:"OWNSCALE_LOG_FORMAT"`
}

// AuthConfig configures user authentication.
type AuthConfig struct {
	SessionSecret string             `yaml:"session_secret" env:"OWNSCALE_AUTH_SESSION_SECRET"`
	UpstreamOIDC  UpstreamOIDCConfig `yaml:"upstream_oidc"`
	OIDCProvider  OIDCProviderConfig `yaml:"oidc_provider"`
}

// UpstreamOIDCConfig configures the upstream OpenID Connect provider used
// for the admin panel's own login (e.g. Google, or a local Dex instance for
// development). ownscale acts as a standard OIDC relying party against it.
type UpstreamOIDCConfig struct {
	Issuer       string `yaml:"issuer" env:"OWNSCALE_AUTH_UPSTREAM_OIDC_ISSUER"`
	ClientID     string `yaml:"client_id" env:"OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_ID"`
	ClientSecret string `yaml:"client_secret" env:"OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_SECRET"`
	CallbackURL  string `yaml:"callback_url" env:"OWNSCALE_AUTH_UPSTREAM_OIDC_CALLBACK_URL"`
}

type OIDCProviderConfig struct {
	Issuer              string `yaml:"issuer" env:"OWNSCALE_AUTH_OIDC_PROVIDER_ISSUER"`
	SigningKeyPath      string `yaml:"signing_key_path" env:"OWNSCALE_AUTH_OIDC_PROVIDER_SIGNING_KEY_PATH"`
	AccessTokenLifetime string `yaml:"access_token_lifetime" env:"OWNSCALE_AUTH_OIDC_PROVIDER_ACCESS_TOKEN_LIFETIME"`
	IDTokenLifetime     string `yaml:"id_token_lifetime" env:"OWNSCALE_AUTH_OIDC_PROVIDER_ID_TOKEN_LIFETIME"`
}

// DerpConfig points every organization's headscale container at a
// self-hosted DERP relay (see deploy/derper) instead of Tailscale's public
// one. Entirely optional: when Hostname is unset, headscale falls back to
// Tailscale's default public DERP map, unchanged from before this config
// section existed.
type DerpConfig struct {
	// Hostname is the DERP relay's public DNS name (e.g. "derp.example.com",
	// matching deploy/derper's DERP_HOSTNAME). Required to enable a custom
	// DERP map; leave empty to keep using Tailscale's public DERP servers.
	Hostname string `yaml:"hostname" env:"OWNSCALE_DERP_HOSTNAME"`
	// IPv4 is the DERP relay's public IPv4 address, recommended so clients
	// don't need to rely on DNS to reach it.
	IPv4 string `yaml:"ipv4" env:"OWNSCALE_DERP_IPV4"`
	// IPv6 is the DERP relay's public IPv6 address (optional).
	IPv6 string `yaml:"ipv6" env:"OWNSCALE_DERP_IPV6"`
	// STUNPort is the UDP port the relay's STUN server listens on. Defaults
	// to 3478 (matching deploy/derper's default) if unset.
	STUNPort int `yaml:"stun_port" env:"OWNSCALE_DERP_STUN_PORT"`
	// RegionID is an arbitrary, positive ID identifying this custom DERP
	// region in headscale's DERP map. Defaults to 900 if unset (headscale's
	// own derp-example.yaml convention for a first custom region).
	RegionID int `yaml:"region_id" env:"OWNSCALE_DERP_REGION_ID"`
	// RegionCode is a short, lowercase identifier for this region. Defaults
	// to "custom" if unset.
	RegionCode string `yaml:"region_code" env:"OWNSCALE_DERP_REGION_CODE"`
	// RegionName is a human-readable label for this region. Defaults to
	// "Self-hosted DERP" if unset.
	RegionName string `yaml:"region_name" env:"OWNSCALE_DERP_REGION_NAME"`
}

// Enabled reports whether a self-hosted DERP relay is configured.
func (d DerpConfig) Enabled() bool { return d.Hostname != "" }

func defaults() *Config {
	return &Config{
		Addr:       ":8080",
		DataDir:    "data",
		LocalCADir: "local-ca",
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
	// LocalCADir is stored under DataDir unless already absolute.
	if cfg.LocalCADir != "" && cfg.DataDir != "" && !filepath.IsAbs(cfg.LocalCADir) {
		cfg.LocalCADir = filepath.Join(cfg.DataDir, cfg.LocalCADir)
	}
	// Fill in DerpConfig defaults only when a custom relay is actually
	// configured; an unset Hostname must stay a no-op (see DerpConfig.Enabled).
	if cfg.Derp.Enabled() {
		if cfg.Derp.STUNPort == 0 {
			cfg.Derp.STUNPort = 3478
		}
		if cfg.Derp.RegionID == 0 {
			cfg.Derp.RegionID = 900
		}
		if cfg.Derp.RegionCode == "" {
			cfg.Derp.RegionCode = "custom"
		}
		if cfg.Derp.RegionName == "" {
			cfg.Derp.RegionName = "Self-hosted DERP"
		}
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
		return fmt.Errorf(`base_domain must not be "localhost": for local testing use ownscale.test` +
			` and set up your /etc/hosts with:
sudo tee -a /etc/hosts <<'EOF'
127.0.0.1 ownscale.test
127.0.0.1 dex.ownscale.test
127.0.0.1 docs.ownscale.test
# If you're using organization names other than acme, those should be added too.
127.0.0.1 acme.ownscale.test
EOF`)
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
