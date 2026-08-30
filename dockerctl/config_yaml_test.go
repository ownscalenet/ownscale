package dockerctl

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestHeadscaleFileConfigRoundTripsUnknownKeys(t *testing.T) {
	ctl, _ := newProvisionedTestCtl(t)

	// The host file is only a fixture source here (production code never
	// reads config.yaml back from disk -- the database row is the source
	// of truth): read the file CreateOrganization wrote, as a stand-in for
	// what would otherwise be read out of orgconfigs.
	configHostFile := filepath.Join(ctl.headscaleConfigHostDir("acme"), "config.yaml")
	raw, err := os.ReadFile(configHostFile)
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}

	cfg, err := ParseHeadscaleFileConfig(string(raw))
	if err != nil {
		t.Fatalf("ParseHeadscaleFileConfig: %v", err)
	}
	if _, ok := cfg.Rest["database"]; !ok {
		t.Fatalf("Rest missing top-level %q key: %v", "database", cfg.Rest)
	}
	if _, ok := cfg.Rest["server_url"]; !ok {
		t.Fatalf("Rest missing top-level %q key: %v", "server_url", cfg.Rest)
	}
	cfg.OIDC.Rest = map[string]any{"future_field": "kept"}

	updated, err := cfg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := ctl.WriteConfigYAML(t.Context(), "acme", updated); err != nil {
		t.Fatalf("WriteConfigYAML: %v", err)
	}

	raw, err = os.ReadFile(configHostFile)
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse config.yaml: %v", err)
	}
	if _, ok := doc["database"]; !ok {
		t.Errorf("database key dropped from config.yaml after write:\n%s", raw)
	}
	oidc, _ := doc["oidc"].(map[string]any)
	if oidc["future_field"] != "kept" {
		t.Errorf("oidc.future_field = %v, want %q", oidc["future_field"], "kept")
	}
}
