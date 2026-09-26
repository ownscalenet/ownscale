package orgs

import (
	"fmt"
	"regexp"
	"strings"

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

// newFileConfig returns the initial stored config for an organization:
// MagicDNS on its own tailnet domain, and the OIDC client credentials. This
// snapshot is ownscale's source of truth for DNS and OIDC settings; headscale
// itself is configured from it in createHeadscale.
func newFileConfig(subdomain string, oidc OIDCConfig) *HeadscaleFileConfig {
	oidc.Scope = []string{"openid", "profile", "email", "offline_access"}
	oidc.PKCE.Enabled = true
	oidc.PKCE.Method = "S256"
	return &HeadscaleFileConfig{
		DNS: DNSConfig{
			MagicDNS:    true,
			BaseDomain:  subdomain + ".ownscale",
			Nameservers: Nameservers{Global: []string{"1.1.1.1"}},
		},
		OIDC: oidc,
	}
}

// BuildHeadscaleConfig renders the initial stored config YAML for an
// organization (see newFileConfig).
func BuildHeadscaleConfig(subdomain string, oidc OIDCConfig) (_ string, err error) {
	defer derrors.Wrap(&err, "BuildHeadscaleConfig(%q)", subdomain)
	return newFileConfig(subdomain, oidc).Marshal()
}
