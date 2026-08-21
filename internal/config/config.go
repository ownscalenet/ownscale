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

	Google GoogleConfig `yaml:"google"`
}

// GoogleConfig configures the Google OAuth 2.0 provider.
type GoogleConfig struct {
	// ClientID is the OAuth 2.0 client ID. Required to run the server.
	ClientID string `yaml:"client_id" env:"OWNSCALE_AUTH_GOOGLE_CLIENT_ID"`
	// ClientSecret is the OAuth 2.0 client secret. Required to run the server.
	ClientSecret string `yaml:"client_secret" env:"OWNSCALE_AUTH_GOOGLE_CLIENT_SECRET"`
	// CallbackURL must match the redirect URI configured on the OAuth
	// client (e.g. "http://localhost:8080/auth/google/callback"). Required
	// to run the server.
	CallbackURL string `yaml:"callback_url" env:"OWNSCALE_AUTH_GOOGLE_CALLBACK_URL"`
}

func defaults() *Config {
	return &Config{
		Addr:    ":8080",
		DataDir: "data",
		Log: LogConfig{
			Level:  "info",
			Format: "json",
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

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, cfg); err != nil {
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
	if c.Auth.Google.ClientID == "" {
		missing = append(missing, "auth.google.client_id")
	}
	if c.Auth.Google.ClientSecret == "" {
		missing = append(missing, "auth.google.client_secret")
	}
	if c.Auth.Google.CallbackURL == "" {
		missing = append(missing, "auth.google.callback_url")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config values: %s", strings.Join(missing, ", "))
	}
	if c.DataDir != "" && !filepath.IsAbs(c.DataDir) {
		return fmt.Errorf("data_dir must resolve to an absolute path, got %q", c.DataDir)
	}
	return nil
}

func (c Config) Database() string {
	return filepath.Join(c.DataDir, "ownscale.sqlite")
}
