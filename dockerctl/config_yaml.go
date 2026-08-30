package dockerctl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ownscalenet/ownscale/derrors"
	"gopkg.in/yaml.v3"
)

// Most of the headscale config (denoted here as Rest) is generated at provision
// time from a template, but DNS and OIDC can be edited from the frontend.
// We have DNS and OIDC structs to get some type safety, but remember to keep
// them in sync with headscale :(
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
	MagicDNS      bool        `json:"magicDns" yaml:"magic_dns"`
	BaseDomain    string      `json:"baseDomain" yaml:"base_domain"`
	Nameservers   Nameservers `json:"nameservers" yaml:"nameservers"`
	SearchDomains []string    `json:"searchDomains" yaml:"search_domains,omitempty"`
	ExtraRecords  []DNSRecord `json:"extraRecords" yaml:"extra_records,omitempty"`
	// TODO: add GUI for OverrideLocalDNS. As it is, headscale validation
	// does not allow OverrideLocalDNS to be true while having no nameservers.
	// OverrideLocalDNS bool
	// Rest inline so if additional fields will exist in the file, they
	// will get preserved when the config is updated.
	Rest map[string]any `json:"-" yaml:",inline"`
}

type Nameservers struct {
	Global []string `json:"global" yaml:"global"`
	// Rest inline so if additional fields will exist in the file, they
	// will get preserved when the config is updated.
	Rest map[string]any `json:"-" yaml:",inline"`
}

// OIDCConfig mirrors headscale's oidc config block, covering every
// field BuildHeadscaleConfig writes plus the allowlist fields
// orgs.Service.SetAuthRestrictions patches in. Rest defensively preserves
// any oidc key added by a future headscale version that ownscale doesn't
// know about yet.
type OIDCConfig struct {
	Issuer       string   `yaml:"issuer"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Scope        []string `yaml:"scope,omitempty"`
	PKCE         struct {
		Enabled bool   `yaml:"enabled"`
		Method  string `yaml:"method,omitempty"`
	} `yaml:"pkce"`
	AllowedDomains []string `yaml:"allowed_domains,omitempty"`
	AllowedGroups  []string `yaml:"allowed_groups,omitempty"`
	AllowedUsers   []string `yaml:"allowed_users,omitempty"`

	// Rest inline so if additional fields will exist in the file, they
	// will get preserved when the config is updated.
	Rest map[string]any `json:"-" yaml:",inline"`
}

func (c *Ctl) headscaleConfigHostFile(subdomain string) string {
	return filepath.Join(c.headscaleConfigHostDir(subdomain), "config.yaml")
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

func (c *Ctl) WriteConfigYAML(ctx context.Context, subdomain, content string) (err error) {
	defer derrors.Wrap(&err, "c.WriteConfigYAML(%q)", subdomain)
	if err := os.WriteFile(c.headscaleConfigHostFile(subdomain), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}
