package server

import "testing"

func TestClassifyHost(t *testing.T) {
	const (
		base    = "ownscale.test"
		content = "content.net"
	)
	tests := []struct {
		name          string
		host          string
		baseDomain    string
		contentDomain string
		want          hostRoute
	}{
		{"org", "aaaa.ownscale.test", base, content, hostRoute{kind: hostOrg, subdomain: "aaaa"}},
		{"org with port, uppercase", "AAAA.ownscale.test:443", base, content, hostRoute{kind: hostOrg, subdomain: "aaaa"}},
		{"console", "console.ownscale.test", base, content, hostRoute{kind: hostConsole}},
		{"nested under base", "a.aaaa.ownscale.test", base, content, hostRoute{kind: hostConsole}},
		{"funnel", "web-aaaa.content.net", base, content, hostRoute{kind: hostFunnel, subdomain: "aaaa", label: "web"}},
		{"funnel on base domain", "web-aaaa.ownscale.test", base, base, hostRoute{kind: hostFunnel, subdomain: "aaaa", label: "web"}},
		{"funnel host on base with separate content domain", "web-aaaa.ownscale.test", base, content, hostRoute{kind: hostConsole}},
		{"org name under content domain", "aaaa.content.net", base, content, hostRoute{kind: hostConsole}},
		{"node name", "web.netaaaa.os.content.net", base, content, hostRoute{kind: hostConsole}},
		{"docs", "ownscale.test", base, content, hostRoute{kind: hostDocs}},
		{"docs with port", "ownscale.test:8080", base, content, hostRoute{kind: hostDocs}},
		{"landing", "content.net", base, content, hostRoute{kind: hostLanding}},
		{"docs when content is base", "ownscale.test", base, base, hostRoute{kind: hostDocs}},
		{"suffix lookalike", "evilownscale.test", base, content, hostRoute{kind: hostConsole}},
		{"foreign host", "example.org", base, content, hostRoute{kind: hostConsole}},
		{"no base domain", "aaaa.ownscale.test", "", "", hostRoute{kind: hostConsole}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyHost(tt.host, tt.baseDomain, tt.contentDomain); got != tt.want {
				t.Errorf("classifyHost(%q, %q, %q) = %+v, want %+v", tt.host, tt.baseDomain, tt.contentDomain, got, tt.want)
			}
		})
	}
}
