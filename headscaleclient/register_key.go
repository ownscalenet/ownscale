package headscaleclient

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// authReqKeyPattern matches a full, already-prefixed registration key as
// printed by `tailscale up` against headscale >=0.23 (and expected by
// headscale 0.29.3, the version ownscale pins -- see
// dockerctl/dockerctl.go's Image).
var authReqKeyPattern = regexp.MustCompile(`^hskey-authreq-[A-Za-z0-9_-]{16,}$`)

// legacyMachineKeyPattern matches the older `mkey:...` registration key
// format from pre-0.23 headscale versions.
var legacyMachineKeyPattern = regexp.MustCompile(`^mkey:[A-Za-z0-9_-]{16,}$`)

// bareTokenPattern matches a registration token with no key-format prefix
// at all, e.g. copy-pasted from the middle of the registration URL.
var bareTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,}$`)

// NormalizeRegistrationKey turns a value copy-pasted from `tailscale up`'s
// output (a full "https://.../register/<token>" URL, a bare token, an
// already-prefixed "hskey-authreq-<token>" key, or a legacy "mkey:..."
// key) into the exact key form headscale's register endpoint expects.
// Mirrors headplane's utils/register-key.ts normalizeRegistrationKey.
func NormalizeRegistrationKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("registration key is required")
	}

	if u, err := url.Parse(value); err == nil && u.Path != "" {
		if idx := strings.LastIndex(u.Path, "/register/"); idx != -1 {
			token := u.Path[idx+len("/register/"):]
			if normalized, ok := normalizeToken(token); ok {
				return normalized, nil
			}
		}
	}

	if normalized, ok := normalizeToken(value); ok {
		return normalized, nil
	}
	return "", fmt.Errorf("unrecognized registration key format")
}

// normalizeToken normalizes a single token (with no surrounding URL) into
// headscale's expected key form.
func normalizeToken(token string) (string, bool) {
	token = strings.TrimSpace(token)
	switch {
	case authReqKeyPattern.MatchString(token):
		return token, true
	case legacyMachineKeyPattern.MatchString(token):
		return token, true
	case bareTokenPattern.MatchString(token):
		return "hskey-authreq-" + token, true
	default:
		return "", false
	}
}
