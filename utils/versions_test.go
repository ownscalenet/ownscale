package utils

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	tailscaleRequire = regexp.MustCompile(`(?m)^\s*tailscale\.com\s+v(\d+)\.(\d+)\.\S+`)
	goDirective      = regexp.MustCompile(`(?m)^go\s+(\d+)\.(\d+)(?:\.(\d+))?\s*$`)
	npmVersion       = regexp.MustCompile(`^(\d+)\.(\d+)\.`)
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// tailscaleVersion returns the full version and the major.minor of the
// tailscale.com requirement in a go.mod.
func tailscaleVersion(t *testing.T, gomod string) (full, minor string) {
	t.Helper()
	m := tailscaleRequire.FindStringSubmatch(gomod)
	if m == nil {
		t.Fatal("no tailscale.com requirement in go.mod")
	}
	return strings.Fields(m[0])[1], m[1] + "." + m[2]
}

// goVersion returns the go directive of a go.mod as comparable numbers.
func goVersion(t *testing.T, gomod string) [3]int {
	t.Helper()
	m := goDirective.FindStringSubmatch(gomod)
	if m == nil {
		t.Fatal("no go directive in go.mod")
	}
	var v [3]int
	for i := range v {
		if m[i+1] != "" {
			v[i], _ = strconv.Atoi(m[i+1])
		}
	}
	return v
}

// TestVersionsAgree checks versions that must move together:
//   - tailscale.com must be exactly the version headscale pins, which it
//     is tested against;
//   - the browser's Tailscale client (@tailscale/connect) must share
//     tailscale.com's major.minor, as it speaks the same protocol to
//     headscale;
//   - the go directive must not be older than headscale's.
func TestVersionsAgree(t *testing.T) {
	root := filepath.Join("..")
	gomod := readFile(t, filepath.Join(root, "go.mod"))

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/juanfont/headscale").Output()
	if err != nil {
		t.Fatalf("locating headscale: %v", err)
	}
	hsmod := readFile(t, filepath.Join(strings.TrimSpace(string(out)), "go.mod"))

	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(root, "frontend", "package.json"))), &pkg); err != nil {
		t.Fatal(err)
	}
	connect := pkg.Dependencies["@tailscale/connect"]
	m := npmVersion.FindStringSubmatch(connect)
	if m == nil {
		t.Fatalf("@tailscale/connect version %q in frontend/package.json is not pinned as major.minor.*", connect)
	}

	tsFull, tsMinor := tailscaleVersion(t, gomod)
	hsFull, _ := tailscaleVersion(t, hsmod)

	tests := []struct {
		name      string
		got, want string
	}{
		{"tailscale.com matches headscale's", tsFull, hsFull},
		{"@tailscale/connect matches tailscale.com's major.minor", m[1] + "." + m[2], tsMinor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %s, want %s", tt.got, tt.want)
			}
		})
	}

	t.Run("go directive is not older than headscale's", func(t *testing.T) {
		ours, theirs := goVersion(t, gomod), goVersion(t, hsmod)
		for i := range ours {
			if ours[i] != theirs[i] {
				if ours[i] < theirs[i] {
					t.Errorf("go %v is older than headscale's go %v", ours, theirs)
				}
				return
			}
		}
	})
}
