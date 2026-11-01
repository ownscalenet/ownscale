package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if len(cfg.FixedSubdomains) != 0 {
		t.Errorf("FixedSubdomains = %v, want none by default", cfg.FixedSubdomains)
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

func TestLoadGitHubConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want GitHubConfig
	}{
		{
			name: "unset is disabled",
		},
		{
			name: "configured",
			env: map[string]string{
				"OWNSCALE_AUTH_GITHUB_CLIENT_ID":     "github-client-id",
				"OWNSCALE_AUTH_GITHUB_CLIENT_SECRET": "github-client-secret",
				"OWNSCALE_AUTH_GITHUB_CALLBACK_URL":  "https://example.com/auth/github/callback",
			},
			want: GitHubConfig{
				ClientID:     "github-client-id",
				ClientSecret: "github-client-secret",
				CallbackURL:  "https://example.com/auth/github/callback",
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
			wantEnabled := tc.want != GitHubConfig{}
			if got := cfg.Auth.GitHub.Enabled(); got != wantEnabled {
				t.Errorf("Auth.GitHub.Enabled() = %v, want %v", got, wantEnabled)
			}
			if cfg.Auth.GitHub != tc.want {
				t.Errorf("Auth.GitHub = %+v, want %+v", cfg.Auth.GitHub, tc.want)
			}
		})
	}
}

// TestLoadDerpConfig covers OWNSCALE_DERP_MAP_URL and its default.
func TestLoadDerpConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want DerpConfig
	}{
		{
			name: "default map URL",
			want: DerpConfig{MapURL: DefaultDERPMapURL},
		},
		{
			name: "custom map URL",
			env:  map[string]string{"OWNSCALE_DERP_MAP_URL": "https://derp.example.com/map"},
			want: DerpConfig{MapURL: "https://derp.example.com/map"},
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
			if cfg.Derp != tc.want {
				t.Errorf("Derp = %+v, want %+v", cfg.Derp, tc.want)
			}
		})
	}
}

func TestLoadBackupConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want BackupConfig
	}{
		{
			name: "disabled by default",
			want: BackupConfig{SyncInterval: 10 * time.Second},
		},
		{
			name: "s3 replica",
			env: map[string]string{
				"OWNSCALE_BACKUP_REPLICA_URL":       "s3://bucket/ownscale",
				"OWNSCALE_BACKUP_ACCESS_KEY_ID":     "key",
				"OWNSCALE_BACKUP_SECRET_ACCESS_KEY": "secret",
				"OWNSCALE_BACKUP_REGION":            "eu-central-1",
				"OWNSCALE_BACKUP_ENDPOINT":          "https://s3.example.com",
				"OWNSCALE_BACKUP_SYNC_INTERVAL":     "1m",
			},
			want: BackupConfig{
				ReplicaURL:      "s3://bucket/ownscale",
				AccessKeyID:     "key",
				SecretAccessKey: "secret",
				Region:          "eu-central-1",
				Endpoint:        "https://s3.example.com",
				SyncInterval:    time.Minute,
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
			if cfg.Backup != tc.want {
				t.Errorf("Backup = %+v, want %+v", cfg.Backup, tc.want)
			}
			if got, want := cfg.Backup.Enabled(), tc.want.ReplicaURL != ""; got != want {
				t.Errorf("Enabled() = %v, want %v", got, want)
			}
		})
	}
}

func TestBackupConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     BackupConfig
		wantErr bool
	}{
		{name: "disabled", cfg: BackupConfig{}},
		{name: "enabled", cfg: BackupConfig{ReplicaURL: "file:///backups", SyncInterval: time.Second}},
		{name: "zero sync interval", cfg: BackupConfig{ReplicaURL: "file:///backups"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
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
	cfg.ContentDomain = "example-content.net"
	cfg.DNS.Zone = "example-content.net"
	return cfg
}

func TestValidate(t *testing.T) {
	t.Run("fully populated passes", func(t *testing.T) {
		if err := fullyPopulatedConfig().Validate(); err != nil {
			t.Errorf("Validate: %v", err)
		}
	})

	t.Run("upstream oidc unset still passes (optional)", func(t *testing.T) {
		cfg := fullyPopulatedConfig()
		cfg.Auth.UpstreamOIDC = UpstreamOIDCConfig{}
		if err := cfg.Validate(); err != nil {
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

	debugAddrs := []struct {
		addr    string
		wantErr bool
	}{
		{"", false},
		{"127.0.0.1:6060", false},
		{"[::1]:6060", false},
		{":6060", true},
		{"0.0.0.0:6060", true},
		{"localhost:6060", true},
		{"10.0.0.1:6060", true},
		{"127.0.0.1", true},
	}
	for _, tt := range debugAddrs {
		t.Run("debug_addr "+tt.addr, func(t *testing.T) {
			cfg := fullyPopulatedConfig()
			cfg.DebugAddr = tt.addr
			if err := cfg.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	rfc2136 := RFC2136DNSConfig{Server: "ns:53", KeyName: "k.", KeyAlg: "hmac-sha256", Key: "c2VjcmV0"}
	domains := []struct {
		name    string
		content string
		zone    string
		wantErr string
	}{
		{name: "zone is content domain", content: "example.net", zone: "example.net"},
		{name: "zone is parent", content: "c.example.net", zone: "example.net"},
		{name: "zone is tailnet domain", content: "example.net", zone: "os.example.net", wantErr: "OWNSCALE_DNS_ZONE"},
		{name: "zone elsewhere", content: "example.net", zone: "other.net", wantErr: "OWNSCALE_DNS_ZONE"},
		{name: "uppercase content domain", content: "Example.net", zone: "example.net", wantErr: "OWNSCALE_CONTENT_DOMAIN"},
	}
	for _, tt := range domains {
		t.Run(tt.name, func(t *testing.T) {
			cfg := fullyPopulatedConfig()
			cfg.ContentDomain = tt.content
			cfg.DNS = DNSConfig{Provider: "rfc2136", Zone: tt.zone, RFC2136: rfc2136}
			err := cfg.Validate()
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}

	fields := []struct {
		name   string
		breakF func(*Config)
		want   string
	}{
		{"OWNSCALE_BASE_DOMAIN", func(c *Config) { c.BaseDomain = "" }, "OWNSCALE_BASE_DOMAIN"},
		{"OWNSCALE_DATA_DIR", func(c *Config) { c.DataDir = "" }, "OWNSCALE_DATA_DIR"},
		{"OWNSCALE_AUTH_SESSION_SECRET", func(c *Config) { c.Auth.SessionSecret = "" }, "OWNSCALE_AUTH_SESSION_SECRET"},
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

func TestLoadDNSConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want DNSConfig
	}{
		{
			name: "defaults",
			want: DNSConfig{Provider: "none", Zone: "example.com", PropagationTimeout: 2 * time.Minute, RFC2136: RFC2136DNSConfig{KeyAlg: "hmac-sha256"}},
		},
		{
			// compose.yaml passes unset settings as empty variables.
			name: "empty variable keeps default",
			env:  map[string]string{"OWNSCALE_DNS_RFC2136_KEY_ALG": ""},
			want: DNSConfig{Provider: "none", Zone: "example.com", PropagationTimeout: 2 * time.Minute, RFC2136: RFC2136DNSConfig{KeyAlg: "hmac-sha256"}},
		},
		{
			name: "zone defaults to content domain",
			env:  map[string]string{"OWNSCALE_CONTENT_DOMAIN": "example.net"},
			want: DNSConfig{Provider: "none", Zone: "example.net", PropagationTimeout: 2 * time.Minute, RFC2136: RFC2136DNSConfig{KeyAlg: "hmac-sha256"}},
		},
		{
			name: "overrides",
			env: map[string]string{
				"OWNSCALE_DNS_PROVIDER":             "cloudflare",
				"OWNSCALE_DNS_ZONE":                 "corp.com",
				"OWNSCALE_DNS_PROPAGATION_TIMEOUT":  "0s",
				"OWNSCALE_DNS_CLOUDFLARE_API_TOKEN": "tok",
			},
			want: DNSConfig{
				Provider: "cloudflare", Zone: "corp.com",
				Cloudflare: CloudflareDNSConfig{APIToken: "tok"},
				RFC2136:    RFC2136DNSConfig{KeyAlg: "hmac-sha256"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OWNSCALE_BASE_DOMAIN", "example.com")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.DNS != tc.want {
				t.Errorf("DNS = %+v, want %+v", cfg.DNS, tc.want)
			}
		})
	}
}

func TestDNSConfigValidate(t *testing.T) {
	base := DNSConfig{Provider: "none", Zone: "example.com"}
	rfc2136 := RFC2136DNSConfig{Server: "ns:53", KeyName: "k.", KeyAlg: "hmac-sha256", Key: "c2VjcmV0"}
	cases := []struct {
		name    string
		change  func(*DNSConfig)
		wantErr string
	}{
		{name: "none"},
		{name: "unknown provider", change: func(d *DNSConfig) { d.Provider = "route53" }, wantErr: "OWNSCALE_DNS_PROVIDER"},
		{name: "missing credentials", change: func(d *DNSConfig) { d.Provider = "azure"; d.Azure.TenantID = "t" }, wantErr: "OWNSCALE_DNS_AZURE_CLIENT_SECRET"},
		{name: "cloudflare", change: func(d *DNSConfig) { d.Provider = "cloudflare"; d.Cloudflare.APIToken = "x" }},
		{name: "hetzner", change: func(d *DNSConfig) { d.Provider = "hetzner"; d.Hetzner.APIToken = "x" }},
		{name: "rfc2136", change: func(d *DNSConfig) { d.Provider = "rfc2136"; d.RFC2136 = rfc2136 }},
		{name: "uppercase zone", change: func(d *DNSConfig) { d.Zone = "Example.com" }, wantErr: "OWNSCALE_DNS_ZONE"},
		{name: "negative propagation", change: func(d *DNSConfig) {
			d.Provider = "hetzner"
			d.Hetzner.APIToken = "x"
			d.PropagationTimeout = -time.Second
		}, wantErr: "OWNSCALE_DNS_PROPAGATION_TIMEOUT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			if tc.change != nil {
				tc.change(&d)
			}
			err := d.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestChallengeProvider(t *testing.T) {
	cases := []struct {
		name    string
		dns     DNSConfig
		wantNil bool
	}{
		{name: "none", dns: DNSConfig{Provider: "none"}, wantNil: true},
		{name: "cloudflare", dns: DNSConfig{Provider: "cloudflare", Cloudflare: CloudflareDNSConfig{APIToken: "x"}}},
		{name: "hetzner", dns: DNSConfig{Provider: "hetzner", Hetzner: HetznerDNSConfig{APIToken: "x"}}},
		{name: "azure", dns: DNSConfig{Provider: "azure", Azure: AzureDNSConfig{
			SubscriptionID: "s", ResourceGroupName: "r", TenantID: "t", ClientID: "c", ClientSecret: "x",
		}}},
		{name: "rfc2136", dns: DNSConfig{Provider: "rfc2136", RFC2136: RFC2136DNSConfig{
			Server: "ns:53", KeyName: "k.", KeyAlg: "hmac-sha256", Key: "c2VjcmV0",
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := tc.dns.ChallengeProvider()
			if err != nil {
				t.Fatalf("ChallengeProvider: %v", err)
			}
			if (p == nil) != tc.wantNil {
				t.Errorf("ChallengeProvider() = %v, want nil %v", p, tc.wantNil)
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
