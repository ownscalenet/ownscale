// Command releaseassets generates deployment files from a release checkout.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9][A-Za-z0-9.-]*)?$`)

func main() {
	tag := flag.String("tag", "", "release tag (vX.Y.Z, optionally with a prerelease suffix)")
	source := flag.String("source", ".", "release source directory")
	output := flag.String("output", "", "output directory")
	flag.Parse()
	if err := generate(*tag, *source, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(tag, source, output string) error {
	if !releaseTag.MatchString(tag) || len(tag) > 128 {
		return fmt.Errorf("invalid release tag %q", tag)
	}
	if output == "" {
		return errors.New("output directory is required")
	}
	compose, err := os.ReadFile(filepath.Join(source, "compose.yaml"))
	if err != nil {
		return err
	}
	env, err := os.ReadFile(filepath.Join(source, "example.env"))
	// Older release tags use the previous template name.
	if errors.Is(err, os.ErrNotExist) {
		env, err = os.ReadFile(filepath.Join(source, ".env.example"))
	}
	if err != nil {
		return err
	}
	compose, env, err = render(tag, compose, env)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "compose.yaml"), compose, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, "example.env"), env, 0o644)
}

func render(tag string, compose, env []byte) ([]byte, []byte, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(compose))
	if err := decoder.Decode(&doc); err != nil {
		return nil, nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, nil, errors.New("expected exactly one Compose document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, errors.New("expected a Compose mapping")
	}
	root := doc.Content[0]
	// Decode once into a map too, so duplicate mapping keys fail explicitly.
	var check map[string]any
	if err := root.Decode(&check); err != nil {
		return nil, nil, err
	}
	for _, key := range []string{"include", "configs", "secrets"} {
		if field(root, key) != nil {
			return nil, nil, fmt.Errorf("unsupported Compose field %q", key)
		}
	}
	services := field(root, "services")
	if services == nil || services.Kind != yaml.MappingNode || len(services.Content) != 4 {
		return nil, nil, errors.New("expected only ownscale and caddy services")
	}
	for name, image := range map[string]string{"ownscale": "ownscale", "caddy": "ownscale-caddy"} {
		service := field(services, name)
		if service == nil || service.Kind != yaml.MappingNode {
			return nil, nil, fmt.Errorf("missing service %q", name)
		}
		imageNode := field(service, "image")
		if imageNode == nil || imageNode.Kind != yaml.ScalarNode || field(service, "build") == nil {
			return nil, nil, fmt.Errorf("service %q must have image and build fields", name)
		}
		for _, key := range []string{"extends", "env_file", "develop", "label_file"} {
			if field(service, key) != nil {
				return nil, nil, fmt.Errorf("service %q has unsupported field %q", name, key)
			}
		}
		if err := validateVolumes(field(service, "volumes")); err != nil {
			return nil, nil, fmt.Errorf("service %q: %w", name, err)
		}
		imageNode.Value = "ghcr.io/ownscalenet/" + image + ":" + tag
		removeField(service, "build")
	}
	root.HeadComment = "Release deployment: copy example.env to .env and configure it before starting."
	root.Content[0].HeadComment = ""
	var result bytes.Buffer
	encoder := yaml.NewEncoder(&result)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, nil, err
	}
	const marker = "# --- Local development (uncomment) ---"
	production, _, found := strings.Cut(string(env), marker)
	if !found {
		return nil, nil, errors.New("missing local development boundary in environment template")
	}
	_, production, found = strings.Cut(production, "\n#\n")
	if !found {
		return nil, nil, errors.New("missing introduction boundary in environment template")
	}
	production = "# Copy example.env to .env and fill in real values.\n# Images are pinned in compose.yaml; download matching files for upgrades.\n#\n" + production
	return result.Bytes(), []byte(strings.TrimRight(production, "\n") + "\n"), nil
}

func field(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func removeField(mapping *yaml.Node, key string) {
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func validateVolumes(volumes *yaml.Node) error {
	if volumes == nil {
		return nil
	}
	if volumes.Kind != yaml.SequenceNode {
		return errors.New("expected a volume list")
	}
	for _, volume := range volumes.Content {
		if volume.Kind != yaml.ScalarNode {
			return errors.New("unsupported long-form volume; review release portability")
		}
		source, _, _ := strings.Cut(volume.Value, ":")
		if strings.HasPrefix(source, ".") || (!strings.HasPrefix(source, "/") && !strings.HasPrefix(source, "${") && strings.Contains(source, "/")) {
			return fmt.Errorf("repository-relative volume %q", source)
		}
	}
	return nil
}
