package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsOnly(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, ":8080")
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v, want {info json}", cfg.Log)
	}
	wantDataDir, err := filepath.Abs("data")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want %q (absolute)", cfg.DataDir, wantDataDir)
	}
	if cfg.Development {
		t.Errorf("Development = %v, want false by default", cfg.Development)
	}
}

func TestLoadEnvOverridesDefaults(t *testing.T) {
	t.Setenv("OWNSCALE_ADDR", ":7070")
	t.Setenv("OWNSCALE_BASE_DOMAIN", "override.example.com")
	t.Setenv("OWNSCALE_LOG_LEVEL", "warn")
	t.Setenv("OWNSCALE_LOCAL_CA_DIR", "custom-local-ca")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":7070" {
		t.Errorf("Addr = %q, want %q (env override)", cfg.Addr, ":7070")
	}
	if cfg.BaseDomain != "override.example.com" {
		t.Errorf("BaseDomain = %q, want %q (env override)", cfg.BaseDomain, "override.example.com")
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("Log.Level = %q, want %q (env override of default)", cfg.Log.Level, "warn")
	}
	wantLocalCADir := filepath.Join(cfg.DataDir, "custom-local-ca")
	if cfg.LocalCADir != wantLocalCADir {
		t.Errorf("LocalCADir = %q, want %q (env override, resolved under DataDir)", cfg.LocalCADir, wantLocalCADir)
	}
}

func TestLoadOIDCProviderDefaults(t *testing.T) {
	t.Setenv("OWNSCALE_BASE_DOMAIN", "example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "https://console.example.com"; cfg.Auth.OIDCProvider.Issuer != want {
		t.Errorf("Auth.OIDCProvider.Issuer = %q, want %q", cfg.Auth.OIDCProvider.Issuer, want)
	}
	wantKeyPath := filepath.Join(cfg.DataDir, "oidc_signing_key.pem")
	if cfg.Auth.OIDCProvider.SigningKeyPath != wantKeyPath {
		t.Errorf("Auth.OIDCProvider.SigningKeyPath = %q, want %q", cfg.Auth.OIDCProvider.SigningKeyPath, wantKeyPath)
	}
	if cfg.Auth.OIDCProvider.AccessTokenLifetime != "5m" {
		t.Errorf("Auth.OIDCProvider.AccessTokenLifetime = %q, want %q", cfg.Auth.OIDCProvider.AccessTokenLifetime, "5m")
	}
	if cfg.Auth.OIDCProvider.IDTokenLifetime != "1h" {
		t.Errorf("Auth.OIDCProvider.IDTokenLifetime = %q, want %q", cfg.Auth.OIDCProvider.IDTokenLifetime, "1h")
	}
}

func TestLoadOIDCProviderIssuerExplicitOverride(t *testing.T) {
	t.Setenv("OWNSCALE_BASE_DOMAIN", "example.com")
	t.Setenv("OWNSCALE_AUTH_OIDC_PROVIDER_ISSUER", "https://custom-issuer.example.org")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "https://custom-issuer.example.org"; cfg.Auth.OIDCProvider.Issuer != want {
		t.Errorf("Auth.OIDCProvider.Issuer = %q, want %q (explicit override preserved)", cfg.Auth.OIDCProvider.Issuer, want)
	}
}

// TestLoadGoogleConfig covers auth.google (OWNSCALE_AUTH_GOOGLE_*), which is
// disabled when unset and enabled with its three fields set when configured.
func TestLoadGoogleConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want GoogleConfig
	}{
		{
			name: "unset is disabled",
		},
		{
			name: "configured",
			env: map[string]string{
				"OWNSCALE_AUTH_GOOGLE_CLIENT_ID":     "google-client-id",
				"OWNSCALE_AUTH_GOOGLE_CLIENT_SECRET": "google-client-secret",
				"OWNSCALE_AUTH_GOOGLE_CALLBACK_URL":  "https://example.com/auth/google/callback",
			},
			want: GoogleConfig{
				ClientID:     "google-client-id",
				ClientSecret: "google-client-secret",
				CallbackURL:  "https://example.com/auth/google/callback",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			wantEnabled := tc.want != GoogleConfig{}
			if got := cfg.Auth.Google.Enabled(); got != wantEnabled {
				t.Errorf("Auth.Google.Enabled() = %v, want %v", got, wantEnabled)
			}
			if cfg.Auth.Google != tc.want {
				t.Errorf("Auth.Google = %+v, want %+v", cfg.Auth.Google, tc.want)
			}
		})
	}
}

// TestLoadMicrosoftConfig covers auth.microsoft (OWNSCALE_AUTH_MICROSOFT_*),
// which follows the same "disabled when unset, enabled with these fields
// when configured" shape as TestLoadGoogleConfig.
func TestLoadMicrosoftConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want MicrosoftConfig
	}{
		{
			name: "unset is disabled",
		},
		{
			name: "configured",
			env: map[string]string{
				"OWNSCALE_AUTH_MICROSOFT_CLIENT_ID":     "microsoft-client-id",
				"OWNSCALE_AUTH_MICROSOFT_CLIENT_SECRET": "microsoft-client-secret",
				"OWNSCALE_AUTH_MICROSOFT_CALLBACK_URL":  "https://example.com/auth/microsoft/callback",
			},
			want: MicrosoftConfig{
				ClientID:     "microsoft-client-id",
				ClientSecret: "microsoft-client-secret",
				CallbackURL:  "https://example.com/auth/microsoft/callback",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			wantEnabled := tc.want != MicrosoftConfig{}
			if got := cfg.Auth.Microsoft.Enabled(); got != wantEnabled {
				t.Errorf("Auth.Microsoft.Enabled() = %v, want %v", got, wantEnabled)
			}
			if cfg.Auth.Microsoft != tc.want {
				t.Errorf("Auth.Microsoft = %+v, want %+v", cfg.Auth.Microsoft, tc.want)
			}
		})
	}
}

// TestLoadDerpConfig covers derp.* (OWNSCALE_DERP_*), which is a no-op when
// unset, fills in defaults for optional fields when only hostname is set,
// and preserves any explicitly-set fields.
func TestLoadDerpConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want DerpConfig
	}{
		{
			name: "unset is a no-op",
		},
		{
			name: "fills defaults when hostname set",
			env: map[string]string{
				"OWNSCALE_DERP_HOSTNAME": "derp.example.com",
			},
			want: DerpConfig{
				Hostname:   "derp.example.com",
				STUNPort:   3478,
				RegionID:   900,
				RegionCode: "custom",
				RegionName: "Self-hosted DERP",
			},
		},
		{
			name: "explicit overrides preserved",
			env: map[string]string{
				"OWNSCALE_DERP_HOSTNAME":    "derp.example.com",
				"OWNSCALE_DERP_IPV4":        "198.51.100.1",
				"OWNSCALE_DERP_IPV6":        "2001:db8::1",
				"OWNSCALE_DERP_STUN_PORT":   "4000",
				"OWNSCALE_DERP_REGION_ID":   "901",
				"OWNSCALE_DERP_REGION_CODE": "myderp",
				"OWNSCALE_DERP_REGION_NAME": "My DERP",
			},
			want: DerpConfig{
				Hostname:   "derp.example.com",
				IPv4:       "198.51.100.1",
				IPv6:       "2001:db8::1",
				STUNPort:   4000,
				RegionID:   901,
				RegionCode: "myderp",
				RegionName: "My DERP",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			wantEnabled := tc.want != DerpConfig{}
			if got := cfg.Derp.Enabled(); got != wantEnabled {
				t.Errorf("Derp.Enabled() = %v, want %v", got, wantEnabled)
			}
			if cfg.Derp != tc.want {
				t.Errorf("Derp = %+v, want %+v", cfg.Derp, tc.want)
			}
		})
	}
}

func fullyPopulatedConfig() *Config {
	cfg := defaults()
	cfg.BaseDomain = "example.com"
	cfg.DataDir = "/abs/data"
	cfg.Auth.SessionSecret = "secret"
	cfg.Auth.UpstreamOIDC.Issuer = "https://accounts.google.com"
	cfg.Auth.UpstreamOIDC.ClientID = "client-id"
	cfg.Auth.UpstreamOIDC.ClientSecret = "client-secret"
	cfg.Auth.UpstreamOIDC.CallbackURL = "https://example.com/callback"
	cfg.Auth.OIDCProvider.Issuer = "https://auth.example.com"
	cfg.Auth.OIDCProvider.SigningKeyPath = "/abs/data/oidc_signing_key.pem"
	return cfg
}

func TestValidate(t *testing.T) {
	t.Run("fully populated passes", func(t *testing.T) {
		if err := fullyPopulatedConfig().Validate(); err != nil {
			t.Errorf("Validate: %v", err)
		}
	})

	t.Run("relative data_dir rejected", func(t *testing.T) {
		cfg := fullyPopulatedConfig()
		cfg.DataDir = "relative/data"
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate: want error for relative data_dir, got nil")
		}
		if !strings.Contains(err.Error(), "absolute") {
			t.Errorf("Validate error = %q, want it to mention 'absolute'", err.Error())
		}
	})

	t.Run("base_domain localhost rejected", func(t *testing.T) {
		for _, v := range []string{"localhost", "LOCALHOST", "LocalHost"} {
			cfg := fullyPopulatedConfig()
			cfg.BaseDomain = v
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate: want error for base_domain %q, got nil", v)
			}
			if !strings.Contains(err.Error(), "localhost") {
				t.Errorf("Validate error = %q, want it to mention %q", err.Error(), "localhost")
			}
		}
	})

	fields := []struct {
		name   string
		breakF func(*Config)
		want   string
	}{
		{"OWNSCALE_BASE_DOMAIN", func(c *Config) { c.BaseDomain = "" }, "OWNSCALE_BASE_DOMAIN"},
		{"OWNSCALE_DATA_DIR", func(c *Config) { c.DataDir = "" }, "OWNSCALE_DATA_DIR"},
		{"OWNSCALE_AUTH_SESSION_SECRET", func(c *Config) { c.Auth.SessionSecret = "" }, "OWNSCALE_AUTH_SESSION_SECRET"},
		{"OWNSCALE_AUTH_UPSTREAM_OIDC_ISSUER", func(c *Config) { c.Auth.UpstreamOIDC.Issuer = "" }, "OWNSCALE_AUTH_UPSTREAM_OIDC_ISSUER"},
		{"OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_ID", func(c *Config) { c.Auth.UpstreamOIDC.ClientID = "" }, "OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_ID"},
		{"OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_SECRET", func(c *Config) { c.Auth.UpstreamOIDC.ClientSecret = "" }, "OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_SECRET"},
		{"OWNSCALE_AUTH_UPSTREAM_OIDC_CALLBACK_URL", func(c *Config) { c.Auth.UpstreamOIDC.CallbackURL = "" }, "OWNSCALE_AUTH_UPSTREAM_OIDC_CALLBACK_URL"},
		{"OWNSCALE_AUTH_OIDC_PROVIDER_ISSUER", func(c *Config) { c.Auth.OIDCProvider.Issuer = "" }, "OWNSCALE_AUTH_OIDC_PROVIDER_ISSUER"},
		{"OWNSCALE_AUTH_OIDC_PROVIDER_SIGNING_KEY_PATH", func(c *Config) { c.Auth.OIDCProvider.SigningKeyPath = "" }, "OWNSCALE_AUTH_OIDC_PROVIDER_SIGNING_KEY_PATH"},
	}
	for _, f := range fields {
		t.Run("missing "+f.name, func(t *testing.T) {
			cfg := fullyPopulatedConfig()
			f.breakF(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate: want error when %s is missing, got nil", f.name)
			}
			if !strings.Contains(err.Error(), f.want) {
				t.Errorf("Validate error = %q, want it to mention %q", err.Error(), f.want)
			}
		})
	}
}

func TestDatabase(t *testing.T) {
	cfg := &Config{DataDir: "/tmp/ownscale-data"}
	want := filepath.Join("/tmp/ownscale-data", "ownscale.sqlite")
	if got := cfg.Database(); got != want {
		t.Errorf("Database() = %q, want %q", got, want)
	}
}
