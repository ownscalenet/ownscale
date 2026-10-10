package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const testCompose = `name: ownscale
services:
  ownscale:
    image: ownscale:latest
    build: .
    environment:
      SECRET: ${SESSION_SECRET:?required}
    volumes:
      - ${OWNSCALE_DATA_DIR}:${OWNSCALE_DATA_DIR}
  caddy:
    image: ownscale-caddy:latest
    build:
      context: .
      dockerfile: etc/caddy/Dockerfile
    ports: ["443:443"]
    volumes: ["caddy-data:/data"]
volumes:
  caddy-data:
    name: caddy-data
`

const testEnv = `# Copy to .env and configure.
# Local development instructions.
#
BASE_DOMAIN=example.com
SESSION_SECRET=changeme
# --- Local development (uncomment) ---
# COMPOSE_FILE=compose.yaml:compose.dev.yaml
# DEV_SECRET=dev-only
`

func TestGenerate(t *testing.T) {
	for _, tc := range []struct {
		tag      string
		template string
	}{
		{"v0.1.0", ".env.example"},
		{"v1.2.3-rc.1", "example.env"},
	} {
		t.Run(tc.template, func(t *testing.T) {
			tag := tc.tag
			source, output := t.TempDir(), t.TempDir()
			for name, content := range map[string]string{"compose.yaml": testCompose, tc.template: testEnv, ".env": "SESSION_SECRET=private-secret"} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := generate(tag, source, output); err != nil {
				t.Fatal(err)
			}
			compose, err := os.ReadFile(filepath.Join(output, "compose.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Services map[string]map[string]any `yaml:"services"`
			}
			if err := yaml.Unmarshal(compose, &result); err != nil {
				t.Fatal(err)
			}
			var original, generated map[string]any
			if err := yaml.Unmarshal([]byte(testCompose), &original); err != nil {
				t.Fatal(err)
			}
			if err := yaml.Unmarshal(compose, &generated); err != nil {
				t.Fatal(err)
			}
			for name, service := range original["services"].(map[string]any) {
				config := service.(map[string]any)
				delete(config, "build")
				config["image"] = result.Services[name]["image"]
			}
			if !reflect.DeepEqual(original, generated) {
				t.Fatal("configuration changed beyond image and build fields")
			}
			for service, image := range map[string]string{"ownscale": "ownscale", "caddy": "ownscale-caddy"} {
				if got := result.Services[service]["image"]; got != "ghcr.io/ownscalenet/"+image+":"+tag {
					t.Fatalf("wrong image: %v", got)
				}
				if _, ok := result.Services[service]["build"]; ok {
					t.Fatal("build field retained")
				}
			}
			if !strings.Contains(string(compose), "${SESSION_SECRET:?required}") || !strings.Contains(string(compose), "${OWNSCALE_DATA_DIR}:${OWNSCALE_DATA_DIR}") {
				t.Fatal("interpolation was changed")
			}
			env, err := os.ReadFile(filepath.Join(output, "example.env"))
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"private-secret", "COMPOSE_FILE", "DEV_SECRET"} {
				if strings.Contains(string(compose)+string(env), forbidden) {
					t.Fatalf("unexpected content %q", forbidden)
				}
			}
			if !strings.Contains(string(env), "SESSION_SECRET=changeme") {
				t.Fatal("production configuration lost")
			}
			entries, err := os.ReadDir(output)
			if err != nil || len(entries) != 2 {
				t.Fatalf("expected exactly two assets: %v %v", entries, err)
			}
		})
	}
}

func TestRenderInvalid(t *testing.T) {
	for _, tc := range []struct {
		name    string
		compose string
		env     string
	}{
		{"malformed", "[", testEnv},
		{"empty", "", testEnv},
		{"extra document", testCompose + "\n---\nname: extra", testEnv},
		{"duplicate", testCompose + "\nname: duplicate", testEnv},
		{"missing service", strings.Replace(testCompose, "  caddy:", "  other:", 1), testEnv},
		{"extra service", strings.Replace(testCompose, "services:", "services:\n  extra: {}", 1), testEnv},
		{"missing image", strings.Replace(testCompose, "    image: ownscale:latest\n", "", 1), testEnv},
		{"missing build", strings.Replace(testCompose, "    build: .\n", "", 1), testEnv},
		{"relative bind", strings.Replace(testCompose, "${OWNSCALE_DATA_DIR}:${OWNSCALE_DATA_DIR}", "./data:/data", 1), testEnv},
		{"external env", strings.Replace(testCompose, "    build: .", "    build: .\n    env_file: .env", 1), testEnv},
		{"include", testCompose + "\ninclude: other.yaml", testEnv},
		{"missing dev boundary", testCompose, "BASE_DOMAIN=example.com"},
		{"missing intro boundary", testCompose, "# --- Local development (uncomment) ---"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := render("v1.2.3", []byte(tc.compose), []byte(tc.env)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestGenerateInvalid(t *testing.T) {
	for _, tag := range []string{"", "latest", "../v1.0.0", "v1.0.0;echo", "v1.0.0+metadata", strings.Repeat("v", 129)} {
		t.Run(tag, func(t *testing.T) {
			if err := generate(tag, t.TempDir(), t.TempDir()); err == nil {
				t.Fatal("expected invalid tag error")
			}
		})
	}
	if err := generate("v1.0.0", t.TempDir(), ""); err == nil {
		t.Fatal("expected missing output error")
	}
	if err := generate("v1.0.0", t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("expected missing source error")
	}
}
