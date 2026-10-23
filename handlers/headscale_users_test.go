package handlers

import (
	"testing"

	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/hscl"
)

func TestEmailLocalPart(t *testing.T) {
	cases := map[string]string{
		"alice@example.com": "alice",
		"Alice@Example.com": "alice",
		"no-at-sign":        "",
		"@example.com":      "",
	}
	for email, want := range cases {
		if got := emailLocalPart(email); got != want {
			t.Errorf("emailLocalPart(%q) = %q, want %q", email, got, want)
		}
	}
}

func TestShortID(t *testing.T) {
	got := shortID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if got != "aaaaaaaa" {
		t.Errorf("shortID = %q, want first 8 chars with dashes stripped", got)
	}
	if got := shortID("short"); got != "short" {
		t.Errorf("shortID(short id) = %q, want unchanged", got)
	}
}

func TestUniqueHeadscaleName(t *testing.T) {
	existing := []hscl.User{{ID: "1", Name: "alice"}, {ID: "2", Name: "alice-google"}}
	tests := []struct {
		name                        string
		candidate, provider, userID string
		existing                    []hscl.User
		want                        string
	}{
		{"no collision", "bob", "google", "user-id", existing, "bob"},
		{"candidate collides", "alice", "microsoft", "user-id", existing, "alice-microsoft"},
		{"provider-qualified collides too", "alice", "google", "12345678-abcd", existing, "alice-google-12345678"},
		{"funnel name reserved", funnelproxy.AgentHeadscaleUserName, "google", "user-id", nil, funnelproxy.AgentHeadscaleUserName + "-google"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := uniqueHeadscaleName(tt.candidate, tt.provider, tt.userID, tt.existing); got != tt.want {
				t.Errorf("uniqueHeadscaleName(%q) = %q, want %q", tt.candidate, got, tt.want)
			}
		})
	}
}
