// Package config loads the admin panel's runtime configuration from
// OWNSCALE_*-prefixed environment variables on top of built-in defaults.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/derrors"

	"github.com/caarlos0/env/v11"
)

// Config is the admin panel's runtime configuration.
// See .env.example for documentation about these fields.
type Config struct {
	Development bool   `env:"OWNSCALE_DEVELOPMENT"`
	Addr        string `env:"OWNSCALE_ADDR"`
	BaseDomain  string `env:"OWNSCALE_BASE_DOMAIN"`
	DataDir     string `env:"OWNSCALE_DATA_DIR"`
	LocalCADir  string `env:"OWNSCALE_LOCAL_CA_DIR"`

	Auth AuthConfig
	Log  LogConfig
	Derp DerpConfig
}

// LogConfig configures the application's structured (slog) logger.
type LogConfig struct {
	Level string `env:"OWNSCALE_LOG_LEVEL"`
	// Format can be "json" or "text".
	Format string `env:"OWNSCALE_LOG_FORMAT"`
}

// AuthConfig configures user authentication.
type AuthConfig struct {
	SessionSecret string `env:"OWNSCALE_AUTH_SESSION_SECRET"`
	UpstreamOIDC  UpstreamOIDCConfig
	Google        GoogleConfig
	Microsoft     MicrosoftConfig
	OIDCProvider  OIDCProviderConfig
}

// UpstreamOIDCConfig configures the upstream OpenID Connect provider used
// for the admin panel's own login (e.g. Google, or a local Dex instance for
// development). ownscale acts as a standard OIDC relying party against it.
type UpstreamOIDCConfig struct {
	Issuer       string `env:"OWNSCALE_AUTH_UPSTREAM_OIDC_ISSUER"`
	ClientID     string `env:"OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_ID"`
	ClientSecret string `env:"OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_SECRET"`
	CallbackURL  string `env:"OWNSCALE_AUTH_UPSTREAM_OIDC_CALLBACK_URL"`
}

type GoogleConfig struct {
	// Leave ClientID empty to disable Google login.
	ClientID     string `env:"OWNSCALE_AUTH_GOOGLE_CLIENT_ID"`
	ClientSecret string `env:"OWNSCALE_AUTH_GOOGLE_CLIENT_SECRET"`
	CallbackURL  string `env:"OWNSCALE_AUTH_GOOGLE_CALLBACK_URL"`
}

func (g GoogleConfig) Enabled() bool { return g.ClientID != "" }

// MicrosoftConfig configures a direct "Sign in with Microsoft" button on
// the login page, using goth's microsoftonline provider (multi-tenant:
// supports both personal Microsoft accounts and work/school Azure AD
// accounts). Entirely optional, like GoogleConfig.
type MicrosoftConfig struct {
	// Leave ClientID empty to disable Microsoft login.
	ClientID     string `env:"OWNSCALE_AUTH_MICROSOFT_CLIENT_ID"`
	ClientSecret string `env:"OWNSCALE_AUTH_MICROSOFT_CLIENT_SECRET"`
	CallbackURL  string `env:"OWNSCALE_AUTH_MICROSOFT_CALLBACK_URL"`
}

func (m MicrosoftConfig) Enabled() bool { return m.ClientID != "" }

type OIDCProviderConfig struct {
	Issuer              string `env:"OWNSCALE_AUTH_OIDC_PROVIDER_ISSUER"`
	SigningKeyPath      string `env:"OWNSCALE_AUTH_OIDC_PROVIDER_SIGNING_KEY_PATH"`
	AccessTokenLifetime string `env:"OWNSCALE_AUTH_OIDC_PROVIDER_ACCESS_TOKEN_LIFETIME"`
	IDTokenLifetime     string `env:"OWNSCALE_AUTH_OIDC_PROVIDER_ID_TOKEN_LIFETIME"`
}

// DerpConfig points every organization's headscale instance at a
// self-hosted DERP relay (see deploy/derper) instead of Tailscale's public
// one. Entirely optional: when Hostname is unset, headscale falls back to
// Tailscale's default public DERP map, unchanged from before this config
// section existed.
type DerpConfig struct {
	// Hostname is the DERP relay's public DNS name (e.g. "derp.example.com",
	// matching deploy/derper's DERP_HOSTNAME). Required to enable a custom
	// DERP map; leave empty to keep using Tailscale's public DERP servers.
	Hostname string `env:"OWNSCALE_DERP_HOSTNAME"`
	// IPv4 is the DERP relay's public IPv4 address, recommended so clients
	// don't need to rely on DNS to reach it.
	IPv4 string `env:"OWNSCALE_DERP_IPV4"`
	// IPv6 is the DERP relay's public IPv6 address (optional).
	IPv6 string `env:"OWNSCALE_DERP_IPV6"`
	// STUNPort is the UDP port the relay's STUN server listens on. Defaults
	// to 3478 (matching deploy/derper's default) if unset.
	STUNPort int `env:"OWNSCALE_DERP_STUN_PORT"`
	// RegionID is an arbitrary, positive ID identifying this custom DERP
	// region in headscale's DERP map. Defaults to 900 if unset (headscale's
	// own derp-example.yaml convention for a first custom region).
	RegionID int `env:"OWNSCALE_DERP_REGION_ID"`
	// RegionCode is a short, lowercase identifier for this region. Defaults
	// to "custom" if unset.
	RegionCode string `env:"OWNSCALE_DERP_REGION_CODE"`
	// RegionName is a human-readable label for this region. Defaults to
	// "Self-hosted DERP" if unset.
	RegionName string `env:"OWNSCALE_DERP_REGION_NAME"`
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

// Load builds the Config from built-in defaults overridden by
// OWNSCALE_*-prefixed environment variables.
func Load() (_ *Config, err error) {
	defer derrors.Wrap(&err, "config.Load()")

	cfg := defaults()

	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parse environment variables: %w", err)
	}

	if cfg.DataDir != "" {
		abs, err := filepath.Abs(cfg.DataDir)
		if err != nil {
			return nil, fmt.Errorf("resolve OWNSCALE_DATA_DIR %q: %w", cfg.DataDir, err)
		}
		cfg.DataDir = abs
	}

	// The OIDC provider issuer defaults to "https://console.<base_domain>"
	// (ownscale itself is bound to the "console" subdomain by
	// etc/caddy/Caddyfile), which can only be computed once base_domain is
	// known (i.e. after env vars have been applied).
	if cfg.Auth.OIDCProvider.Issuer == "" && cfg.BaseDomain != "" {
		cfg.Auth.OIDCProvider.Issuer = "https://console." + cfg.BaseDomain
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
	defer derrors.Wrap(&err, "c.Validate()")

	var missing []string
	if c.BaseDomain == "" {
		missing = append(missing, "OWNSCALE_BASE_DOMAIN")
	}
	if c.DataDir == "" {
		missing = append(missing, "OWNSCALE_DATA_DIR")
	}
	if c.Auth.SessionSecret == "" {
		missing = append(missing, "OWNSCALE_AUTH_SESSION_SECRET")
	}
	if c.Auth.UpstreamOIDC.Issuer == "" {
		missing = append(missing, "OWNSCALE_AUTH_UPSTREAM_OIDC_ISSUER")
	}
	if c.Auth.UpstreamOIDC.ClientID == "" {
		missing = append(missing, "OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_ID")
	}
	if c.Auth.UpstreamOIDC.ClientSecret == "" {
		missing = append(missing, "OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_SECRET")
	}
	if c.Auth.UpstreamOIDC.CallbackURL == "" {
		missing = append(missing, "OWNSCALE_AUTH_UPSTREAM_OIDC_CALLBACK_URL")
	}
	if c.Auth.OIDCProvider.Issuer == "" {
		missing = append(missing, "OWNSCALE_AUTH_OIDC_PROVIDER_ISSUER")
	}
	if c.Auth.OIDCProvider.SigningKeyPath == "" {
		missing = append(missing, "OWNSCALE_AUTH_OIDC_PROVIDER_SIGNING_KEY_PATH")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config values: %s", strings.Join(missing, ", "))
	}
	if strings.EqualFold(c.BaseDomain, "localhost") {
		return fmt.Errorf(`OWNSCALE_BASE_DOMAIN must not be "localhost": for local testing use ownscale.test` +
			` and set up your /etc/hosts with:
sudo tee -a /etc/hosts <<'EOF'
127.0.0.1 ownscale.test
127.0.0.1 console.ownscale.test
127.0.0.1 dex.ownscale.test
# If you're using organization names other than acme, those should be added too.
127.0.0.1 acme.ownscale.test
EOF`)
	}
	if c.DataDir != "" && !filepath.IsAbs(c.DataDir) {
		return fmt.Errorf("OWNSCALE_DATA_DIR must resolve to an absolute path, got %q", c.DataDir)
	}
	if _, err := time.ParseDuration(c.Auth.OIDCProvider.AccessTokenLifetime); err != nil {
		return fmt.Errorf("OWNSCALE_AUTH_OIDC_PROVIDER_ACCESS_TOKEN_LIFETIME: %w", err)
	}
	if _, err := time.ParseDuration(c.Auth.OIDCProvider.IDTokenLifetime); err != nil {
		return fmt.Errorf("OWNSCALE_AUTH_OIDC_PROVIDER_ID_TOKEN_LIFETIME: %w", err)
	}
	return nil
}

func (c Config) Database() string {
	return filepath.Join(c.DataDir, "ownscale.sqlite")
}
