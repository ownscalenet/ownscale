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
	cfg.Auth.Google.ClientID = "client-id"
	cfg.Auth.Google.ClientSecret = "client-secret"
	cfg.Auth.Google.CallbackURL = "https://example.com/callback"
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

	fields := []struct {
		name   string
		breakF func(*Config)
		want   string
	}{
		{"base_domain", func(c *Config) { c.BaseDomain = "" }, "base_domain"},
		{"data_dir", func(c *Config) { c.DataDir = "" }, "data_dir"},
		{"auth.session_secret", func(c *Config) { c.Auth.SessionSecret = "" }, "auth.session_secret"},
		{"auth.google.client_id", func(c *Config) { c.Auth.Google.ClientID = "" }, "auth.google.client_id"},
		{"auth.google.client_secret", func(c *Config) { c.Auth.Google.ClientSecret = "" }, "auth.google.client_secret"},
		{"auth.google.callback_url", func(c *Config) { c.Auth.Google.CallbackURL = "" }, "auth.google.callback_url"},
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
