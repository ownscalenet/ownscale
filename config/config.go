// Package config loads the admin panel's runtime configuration from
// OWNSCALE_*-prefixed environment variables on top of built-in defaults.
package config

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/derrors"

	"github.com/caarlos0/env/v11"
)

// Config is the admin panel's runtime configuration.
// See example.env for documentation about these fields.
type Config struct {
	Development bool   `env:"OWNSCALE_DEVELOPMENT"`
	Addr        string `env:"OWNSCALE_ADDR"`
	BaseDomain  string `env:"OWNSCALE_BASE_DOMAIN"`
	DataDir     string `env:"OWNSCALE_DATA_DIR"`
	LocalCADir  string `env:"OWNSCALE_LOCAL_CA_DIR"`
	// DebugAddr enables a pprof/runtime-metrics server; must be loopback.
	DebugAddr string `env:"OWNSCALE_DEBUG_ADDR"`
	// TLSAskAddr enables the on-demand TLS "ask" endpoint for an external
	// Caddy; it reveals which organizations exist, better not to publish this
	// but then again, orgs can already be queried at http://org.base_domain.
	TLSAskAddr string `env:"OWNSCALE_TLS_ASK_ADDR"`

	Auth   AuthConfig
	Log    LogConfig
	Derp   DerpConfig
	Funnel FunnelConfig
	Backup BackupConfig
}

// BackupConfig configures continuous replication of every SQLite database
// under DataDir. Leave ReplicaURL empty to disable backups.
type BackupConfig struct {
	ReplicaURL      string        `env:"OWNSCALE_BACKUP_REPLICA_URL"`
	AccessKeyID     string        `env:"OWNSCALE_BACKUP_ACCESS_KEY_ID"`
	SecretAccessKey string        `env:"OWNSCALE_BACKUP_SECRET_ACCESS_KEY"`
	Region          string        `env:"OWNSCALE_BACKUP_REGION"`
	Endpoint        string        `env:"OWNSCALE_BACKUP_ENDPOINT"`
	AzureAccountKey string        `env:"OWNSCALE_BACKUP_AZURE_ACCOUNT_KEY"`
	SyncInterval    time.Duration `env:"OWNSCALE_BACKUP_SYNC_INTERVAL"`
}

// Enabled reports whether backups are configured.
func (b BackupConfig) Enabled() bool { return b.ReplicaURL != "" }

// Validate checks that b is either fully disabled or usable.
func (b BackupConfig) Validate() error {
	if !b.Enabled() {
		return nil
	}
	if b.SyncInterval <= 0 {
		return fmt.Errorf("OWNSCALE_BACKUP_SYNC_INTERVAL must be positive, got %s", b.SyncInterval)
	}
	return nil
}

// FunnelConfig configures public funnels.
type FunnelConfig struct {
	// Domain defaults to BaseDomain.
	Domain string `env:"OWNSCALE_FUNNEL_DOMAIN"`
	// MaxBodyBytes limits the size of funnel request bodies.
	MaxBodyBytes int64 `env:"OWNSCALE_FUNNEL_MAX_BODY_BYTES"`
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

func (u UpstreamOIDCConfig) Enabled() bool { return u.ClientID != "" }

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

// DerpConfig configures the DERP map every organization's headscale uses.
type DerpConfig struct {
	// MapURL is where ownscale fetches the DERP map. Defaults to
	// DefaultDERPMapURL; to use a self-hosted relay, serve a map listing it.
	MapURL string `env:"OWNSCALE_DERP_MAP_URL"`
}

// DefaultDERPMapURL is Tailscale's public DERP map.
const DefaultDERPMapURL = "https://controlplane.tailscale.com/derpmap/default"

func defaults() *Config {
	return &Config{
		Addr:       ":8080",
		DataDir:    "data",
		LocalCADir: "local-ca",
		Funnel: FunnelConfig{
			MaxBodyBytes: 32 << 20,
		},
		Backup: BackupConfig{
			SyncInterval: 10 * time.Second,
		},
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

	if cfg.Funnel.Domain == "" {
		cfg.Funnel.Domain = cfg.BaseDomain
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
	if cfg.Derp.MapURL == "" {
		cfg.Derp.MapURL = DefaultDERPMapURL
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
	if c.Auth.OIDCProvider.Issuer == "" {
		missing = append(missing, "OWNSCALE_AUTH_OIDC_PROVIDER_ISSUER")
	}
	if c.Auth.OIDCProvider.SigningKeyPath == "" {
		missing = append(missing, "OWNSCALE_AUTH_OIDC_PROVIDER_SIGNING_KEY_PATH")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config values: %s", strings.Join(missing, ", "))
	}
	if c.DebugAddr != "" && !isLoopbackAddr(c.DebugAddr) {
		return fmt.Errorf("OWNSCALE_DEBUG_ADDR %q must bind a loopback IP", c.DebugAddr)
	}
	if c.Funnel.MaxBodyBytes <= 0 {
		return fmt.Errorf("OWNSCALE_FUNNEL_MAX_BODY_BYTES must be positive, got %d", c.Funnel.MaxBodyBytes)
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
	if err := c.Backup.Validate(); err != nil {
		return err
	}
	if _, err := time.ParseDuration(c.Auth.OIDCProvider.AccessTokenLifetime); err != nil {
		return fmt.Errorf("OWNSCALE_AUTH_OIDC_PROVIDER_ACCESS_TOKEN_LIFETIME: %w", err)
	}
	if _, err := time.ParseDuration(c.Auth.OIDCProvider.IDTokenLifetime); err != nil {
		return fmt.Errorf("OWNSCALE_AUTH_OIDC_PROVIDER_ID_TOKEN_LIFETIME: %w", err)
	}
	return nil
}

// DatabaseFile is ownscale's own database, relative to Config.DataDir.
const DatabaseFile = "ownscale.sqlite"

// Database returns the path of ownscale's own database.
func (c Config) Database() string {
	return filepath.Join(c.DataDir, DatabaseFile)
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
