package orgs

import (
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/derrors"
	"gopkg.in/yaml.v3"
)

const (
	ConsoleSubdomain = "console"

	// ConfigYAMLPath is the orgconfigs key under which the headscale
	// config.yaml snapshot is stored. The YAML content is ownscale's
	// source of truth for DNS and OIDC settings; headscale itself uses the
	// in-process types.Config.
	ConfigYAMLPath = "/etc/headscale/config.yaml"
)

func ConsoleDomain(baseDomain string) string {
	return ConsoleSubdomain + "." + baseDomain
}

// OIDCClientCredentials are the per-organization relying-party credentials
// templated into headscale.
type OIDCClientCredentials struct {
	Issuer       string
	ClientID     string
	ClientSecret string
}

// InvalidConfigError is returned when a headscale config fails validation.
type InvalidConfigError struct{ msg string }

func NewInvalidConfigError(msg string) *InvalidConfigError {
	return &InvalidConfigError{msg: msg}
}

func (e *InvalidConfigError) Error() string { return e.msg }

// HeadscaleFileConfig holds the subset of headscale's config.yaml that
// ownscale reads and writes. The Rest field captures all other keys so
// round-tripping via Marshal does not drop them.
type HeadscaleFileConfig struct {
	DNS  DNSConfig  `yaml:"dns"`
	OIDC OIDCConfig `yaml:"oidc"`

	Rest map[string]any `json:"-" yaml:",inline"`
}

type DNSRecord struct {
	Name  string `json:"name" yaml:"name"`
	Type  string `json:"type" yaml:"type"`
	Value string `json:"value" yaml:"value"`
}

type DNSConfig struct {
	MagicDNS      bool           `json:"magicDns" yaml:"magic_dns"`
	BaseDomain    string         `json:"baseDomain" yaml:"base_domain"`
	Nameservers   Nameservers    `json:"nameservers" yaml:"nameservers"`
	SearchDomains []string       `json:"searchDomains" yaml:"search_domains,omitempty"`
	ExtraRecords  []DNSRecord    `json:"extraRecords" yaml:"extra_records,omitempty"`
	Rest          map[string]any `json:"-" yaml:",inline"`
}

type Nameservers struct {
	Global []string       `json:"global" yaml:"global"`
	Rest   map[string]any `json:"-" yaml:",inline"`
}

// OIDCConfig mirrors headscale's oidc config block.
type OIDCConfig struct {
	Issuer       string   `yaml:"issuer"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Scope        []string `yaml:"scope,omitempty"`
	PKCE         struct {
		Enabled bool   `yaml:"enabled"`
		Method  string `yaml:"method,omitempty"`
	} `yaml:"pkce"`
	AllowedDomains []string       `yaml:"allowed_domains,omitempty"`
	AllowedGroups  []string       `yaml:"allowed_groups,omitempty"`
	AllowedUsers   []string       `yaml:"allowed_users,omitempty"`
	Rest           map[string]any `json:"-" yaml:",inline"`
}

func ParseHeadscaleFileConfig(content string) (*HeadscaleFileConfig, error) {
	var cfg HeadscaleFileConfig
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

func (cfg *HeadscaleFileConfig) Marshal() (string, error) {
	updated, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(updated), nil
}

var subdomainRe = regexp.MustCompile(`^[a-z0-9]{4,63}$`)

var reservedSubdomains = map[string]bool{
	"admin":   true,
	"auth":    true,
	"docs":    true,
	"console": true,
	"derp":    true,
	"www":     true,
}

func ValidateSubdomain(s string) (err error) {
	defer derrors.Wrap(&err, "orgs.ValidateSubdomain(%q)", s)

	if !subdomainRe.MatchString(s) {
		return fmt.Errorf("must contain only lowercase letters and digits, at least 4 characters")
	}
	if reservedSubdomains[strings.ToLower(s)] {
		return fmt.Errorf("%q is a reserved subdomain and cannot be used for an organization", s)
	}
	return nil
}

// configTemplateHS is the template for the headscale config.yaml snapshot
// stored in orgconfigs. This is ownscale's source of truth for DNS/OIDC
// settings; headscale itself is configured via types.Config in-process.
var configTemplateHS = template.Must(template.New("headscaleConfig").Parse(`
server_url: https://{{.ServerURL}}
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
    - /etc/headscale/derp.yaml
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
  base_domain: {{.Subdomain}}.ownscale
  nameservers:
    global:
      - 1.1.1.1

unix_socket: /var/lib/headscale/headscale.sock
unix_socket_permission: "0770"

policy:
  mode: database

oidc:
  issuer: "{{.OIDCIssuer}}"
  client_id: "{{.OIDCClientID}}"
  client_secret: "{{.OIDCClientSecret}}"
  scope: ["openid", "profile", "email", "offline_access"]
  pkce:
    enabled: true
    method: S256
`))

// BuildHeadscaleConfig renders the headscale config.yaml snapshot stored
// in orgconfigs. This snapshot is ownscale's source of truth for DNS and
// OIDC settings; headscale itself uses the in-process types.Config.
func BuildHeadscaleConfig(subdomain, domain string, oidcCreds OIDCClientCredentials, derpCfg config.DerpConfig) (_ string, err error) {
	defer derrors.Wrap(&err, "BuildHeadscaleConfig(%q, %q)", subdomain, domain)

	var buf strings.Builder
	if err := configTemplateHS.Execute(&buf, struct {
		Subdomain        string
		ServerURL        string
		OIDCIssuer       string
		OIDCClientID     string
		OIDCClientSecret string
		HasCustomDerp    bool
	}{
		Subdomain:        subdomain,
		ServerURL:        domain,
		OIDCIssuer:       oidcCreds.Issuer,
		OIDCClientID:     oidcCreds.ClientID,
		OIDCClientSecret: oidcCreds.ClientSecret,
		HasCustomDerp:    derpCfg.Enabled(),
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// configTemplateDerp is the template for the custom DERP relay config.
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

	var buf strings.Builder
	if err := configTemplateDerp.Execute(&buf, derpCfg); err != nil {
		return "", err
	}
	return buf.String(), nil
}
