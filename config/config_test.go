package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withDir chdir's into a fresh temp directory for the duration of the test,
// so DefaultPath ("config.yaml") lookups are hermetic and don't ever touch a
// real /etc/ownscale/config.yaml or a repo-relative file.
func withDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func TestLoadDefaultsOnly(t *testing.T) {
	withDir(t)

	cfg, err := Load("")
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
}

func TestLoadYAMLOverridesDefaults(t *testing.T) {
	dir := withDir(t)

	yaml := `
addr: ":9090"
base_domain: example.com
data_dir: mydata
log:
  level: debug
  format: text
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, ":9090")
	}
	if cfg.BaseDomain != "example.com" {
		t.Errorf("BaseDomain = %q, want %q", cfg.BaseDomain, "example.com")
	}
	if cfg.Log.Level != "debug" || cfg.Log.Format != "text" {
		t.Errorf("Log = %+v, want {debug text}", cfg.Log)
	}
	wantDataDir, err := filepath.Abs("mydata")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
}

func TestLoadEnvOverridesYAMLAndDefaults(t *testing.T) {
	dir := withDir(t)

	yaml := `
addr: ":9090"
base_domain: example.com
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("OWNSCALE_ADDR", ":7070")
	t.Setenv("OWNSCALE_BASE_DOMAIN", "override.example.com")
	t.Setenv("OWNSCALE_LOG_LEVEL", "warn")
	t.Setenv("OWNSCALE_LOCAL_CA_DIR", "custom-local-ca")

	cfg, err := Load("")
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

func TestLoadMissingDefaultPathFallsBack(t *testing.T) {
	withDir(t)
	// No config.yaml written; Load("") should silently proceed with
	// defaults + env vars rather than error.
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want default %q", cfg.Addr, ":8080")
	}
}

func TestLoadOIDCProviderDefaults(t *testing.T) {
	dir := withDir(t)

	yaml := `
base_domain: example.com
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "https://example.com"; cfg.Auth.OIDCProvider.Issuer != want {
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
	dir := withDir(t)

	yaml := `
base_domain: example.com
auth:
  oidc_provider:
    issuer: "https://custom-issuer.example.org"
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "https://custom-issuer.example.org"; cfg.Auth.OIDCProvider.Issuer != want {
		t.Errorf("Auth.OIDCProvider.Issuer = %q, want %q (explicit override preserved)", cfg.Auth.OIDCProvider.Issuer, want)
	}
}

func TestLoadGoogleUnsetIsDisabled(t *testing.T) {
	dir := withDir(t)

	yaml := `
base_domain: example.com
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.Google.Enabled() {
		t.Fatalf("Auth.Google.Enabled() = true, want false when auth.google.client_id is unset")
	}
	if cfg.Auth.Google != (GoogleConfig{}) {
		t.Errorf("Auth.Google = %+v, want zero value when unset", cfg.Auth.Google)
	}
}

func TestLoadGoogleConfigured(t *testing.T) {
	dir := withDir(t)

	yaml := `
base_domain: example.com
auth:
  google:
    client_id: "google-client-id"
    client_secret: "google-client-secret"
    callback_url: "https://example.com/auth/google/callback"
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Auth.Google.Enabled() {
		t.Fatal("Auth.Google.Enabled() = false, want true when auth.google.client_id is set")
	}
	want := GoogleConfig{
		ClientID:     "google-client-id",
		ClientSecret: "google-client-secret",
		CallbackURL:  "https://example.com/auth/google/callback",
	}
	if cfg.Auth.Google != want {
		t.Errorf("Auth.Google = %+v, want %+v", cfg.Auth.Google, want)
	}
}

func TestLoadMicrosoftUnsetIsDisabled(t *testing.T) {
	dir := withDir(t)

	yaml := `
base_domain: example.com
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.Microsoft.Enabled() {
		t.Fatalf("Auth.Microsoft.Enabled() = true, want false when auth.microsoft.client_id is unset")
	}
	if cfg.Auth.Microsoft != (MicrosoftConfig{}) {
		t.Errorf("Auth.Microsoft = %+v, want zero value when unset", cfg.Auth.Microsoft)
	}
}

func TestLoadMicrosoftConfigured(t *testing.T) {
	dir := withDir(t)

	yaml := `
base_domain: example.com
auth:
  microsoft:
    client_id: "microsoft-client-id"
    client_secret: "microsoft-client-secret"
    callback_url: "https://example.com/auth/microsoft/callback"
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Auth.Microsoft.Enabled() {
		t.Fatal("Auth.Microsoft.Enabled() = false, want true when auth.microsoft.client_id is set")
	}
	want := MicrosoftConfig{
		ClientID:     "microsoft-client-id",
		ClientSecret: "microsoft-client-secret",
		CallbackURL:  "https://example.com/auth/microsoft/callback",
	}
	if cfg.Auth.Microsoft != want {
		t.Errorf("Auth.Microsoft = %+v, want %+v", cfg.Auth.Microsoft, want)
	}
}

func TestLoadDerpUnsetIsNoop(t *testing.T) {
	dir := withDir(t)

	yaml := `
base_domain: example.com
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Derp.Enabled() {
		t.Fatalf("Derp.Enabled() = true, want false when derp.hostname is unset")
	}
	if cfg.Derp != (DerpConfig{}) {
		t.Errorf("Derp = %+v, want zero value when unset", cfg.Derp)
	}
}

func TestLoadDerpFillsDefaultsWhenHostnameSet(t *testing.T) {
	dir := withDir(t)

	yaml := `
base_domain: example.com
derp:
  hostname: derp.example.com
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Derp.Enabled() {
		t.Fatal("Derp.Enabled() = false, want true when derp.hostname is set")
	}
	want := DerpConfig{
		Hostname:   "derp.example.com",
		STUNPort:   3478,
		RegionID:   900,
		RegionCode: "custom",
		RegionName: "Self-hosted DERP",
	}
	if cfg.Derp != want {
		t.Errorf("Derp = %+v, want %+v", cfg.Derp, want)
	}
}

func TestLoadDerpExplicitOverridesPreserved(t *testing.T) {
	dir := withDir(t)

	yaml := `
base_domain: example.com
derp:
  hostname: derp.example.com
  ipv4: "198.51.100.1"
  ipv6: "2001:db8::1"
  stun_port: 4000
  region_id: 901
  region_code: "myderp"
  region_name: "My DERP"
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := DerpConfig{
		Hostname:   "derp.example.com",
		IPv4:       "198.51.100.1",
		IPv6:       "2001:db8::1",
		STUNPort:   4000,
		RegionID:   901,
		RegionCode: "myderp",
		RegionName: "My DERP",
	}
	if cfg.Derp != want {
		t.Errorf("Derp = %+v, want %+v (explicit values preserved)", cfg.Derp, want)
	}
}

func TestLoadExplicitMissingPathErrors(t *testing.T) {
	withDir(t)
	_, err := Load("/does/not/exist/config.yaml")
	if err == nil {
		t.Fatal("Load: want error for missing explicit --config path, got nil")
	}
}

func TestLoadMalformedYAMLErrors(t *testing.T) {
	dir := withDir(t)
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("addr: [unterminated"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load: want error for malformed YAML, got nil")
	}
}

func TestLoadUnknownFieldErrors(t *testing.T) {
	dir := withDir(t)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("not_a_real_field: true"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load: want error for unknown YAML field (KnownFields), got nil")
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
		{"base_domain", func(c *Config) { c.BaseDomain = "" }, "base_domain"},
		{"data_dir", func(c *Config) { c.DataDir = "" }, "data_dir"},
		{"auth.session_secret", func(c *Config) { c.Auth.SessionSecret = "" }, "auth.session_secret"},
		{"auth.upstream_oidc.issuer", func(c *Config) { c.Auth.UpstreamOIDC.Issuer = "" }, "auth.upstream_oidc.issuer"},
		{"auth.upstream_oidc.client_id", func(c *Config) { c.Auth.UpstreamOIDC.ClientID = "" }, "auth.upstream_oidc.client_id"},
		{"auth.upstream_oidc.client_secret", func(c *Config) { c.Auth.UpstreamOIDC.ClientSecret = "" }, "auth.upstream_oidc.client_secret"},
		{"auth.upstream_oidc.callback_url", func(c *Config) { c.Auth.UpstreamOIDC.CallbackURL = "" }, "auth.upstream_oidc.callback_url"},
		{"auth.oidc_provider.issuer", func(c *Config) { c.Auth.OIDCProvider.Issuer = "" }, "auth.oidc_provider.issuer"},
		{"auth.oidc_provider.signing_key_path", func(c *Config) { c.Auth.OIDCProvider.SigningKeyPath = "" }, "auth.oidc_provider.signing_key_path"},
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
