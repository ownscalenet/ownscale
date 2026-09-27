package orgs

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ownscalenet/ownscale/derrors"
	"gopkg.in/yaml.v3"
)

const (
	// ConsoleSubdomain is the subdomain serving the ownscale console.
	ConsoleSubdomain = "console"

	// HeadscaleConfigKey is the orgconfigs key under which an organization's
	// HeadscaleConfig is stored as YAML.
	HeadscaleConfigKey = "headscale.yaml"
)

// ConsoleDomain returns the console's domain under baseDomain.
func ConsoleDomain(baseDomain string) string {
	return ConsoleSubdomain + "." + baseDomain
}

// InvalidConfigError is returned when a headscale config fails validation.
type InvalidConfigError struct{ msg string }

// NewInvalidConfigError returns an InvalidConfigError with msg.
func NewInvalidConfigError(msg string) *InvalidConfigError {
	return &InvalidConfigError{msg: msg}
}

func (e *InvalidConfigError) Error() string { return e.msg }

// HeadscaleConfig holds an organization's user-editable headscale settings.
// It is ownscale's source of truth for them; createHeadscale builds
// headscale's types.Config from it.
type HeadscaleConfig struct {
	DNS  DNSConfig  `yaml:"dns"`
	OIDC OIDCConfig `yaml:"oidc"`
}

// DNSRecord is an extra DNS record served to an organization's nodes.
type DNSRecord struct {
	Name  string `json:"name" yaml:"name"`
	Type  string `json:"type" yaml:"type"`
	Value string `json:"value" yaml:"value"`
}

// DNSConfig is an organization's tailnet DNS settings.
type DNSConfig struct {
	MagicDNS      bool        `json:"magicDns" yaml:"magic_dns"`
	BaseDomain    string      `json:"baseDomain" yaml:"base_domain"`
	Nameservers   Nameservers `json:"nameservers" yaml:"nameservers"`
	SearchDomains []string    `json:"searchDomains" yaml:"search_domains,omitempty"`
	ExtraRecords  []DNSRecord `json:"extraRecords" yaml:"extra_records,omitempty"`
}

// Nameservers lists the DNS resolvers pushed to an organization's nodes.
type Nameservers struct {
	Global []string `json:"global" yaml:"global"`
}

// OIDCConfig is an organization's headscale OIDC client and login allowlist.
type OIDCConfig struct {
	Issuer         string   `yaml:"issuer"`
	ClientID       string   `yaml:"client_id"`
	ClientSecret   string   `yaml:"client_secret"`
	AllowedDomains []string `yaml:"allowed_domains,omitempty"`
	AllowedGroups  []string `yaml:"allowed_groups,omitempty"`
	AllowedUsers   []string `yaml:"allowed_users,omitempty"`
}

func parseHeadscaleConfig(content string) (*HeadscaleConfig, error) {
	var cfg HeadscaleConfig
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// Marshal renders cfg as YAML for storage.
func (cfg *HeadscaleConfig) Marshal() (string, error) {
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

// ValidateSubdomain reports whether s can be used as an organization's subdomain.
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

// newHeadscaleConfig returns the initial config for an organization:
// MagicDNS on its own tailnet domain, and the OIDC client credentials.
func newHeadscaleConfig(subdomain string, oidc OIDCConfig) *HeadscaleConfig {
	return &HeadscaleConfig{
		DNS: DNSConfig{
			MagicDNS:    true,
			BaseDomain:  subdomain + ".ownscale",
			Nameservers: Nameservers{Global: []string{"1.1.1.1"}},
		},
		OIDC: oidc,
	}
}
