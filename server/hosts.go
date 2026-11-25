package server

import (
	"net"
	"strings"

	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/orgs"
)

// hostKind is what a request's Host header routes to.
type hostKind int

const (
	hostConsole hostKind = iota
	hostOrg
	hostFunnel
	hostDocs
	hostLanding
	// hostWWW redirects to the domain without "www.".
	hostWWW
	hostNotFound
)

// hostRoute is the result of classifyHost. subdomain is set for hostOrg and
// hostFunnel, label for hostFunnel.
type hostRoute struct {
	kind      hostKind
	subdomain string
	label     string
}

// classifyHost decides which part of ownscale serves host:
//
//	<org>.baseDomain               org's headscale
//	<label>-<org>.contentDomain    funnel
//	baseDomain                     docs site
//	contentDomain                  content domain's landing page
//	www.baseDomain                 redirect to baseDomain
//	www.contentDomain              redirect to contentDomain
//	console.baseDomain             console
//	other names under the domains  not found
//	anything else                  console (e.g. localhost health checks)
//
// Org subdomains and funnel labels can't contain "-", so the first two never
// overlap, even when contentDomain is baseDomain. Without a baseDomain (tests)
// everything is the console.
func classifyHost(host, baseDomain, contentDomain string) hostRoute {
	if baseDomain == "" {
		return hostRoute{kind: hostConsole}
	}
	host = strings.ToLower(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "www."+baseDomain || host == "www."+contentDomain {
		return hostRoute{kind: hostWWW}
	}
	if sub, ok := strings.CutSuffix(host, "."+baseDomain); ok &&
		sub != "" && sub != orgs.ConsoleSubdomain && !strings.ContainsAny(sub, ".-") {
		return hostRoute{kind: hostOrg, subdomain: sub}
	}
	if contentDomain != "" {
		if label, sub, ok := funnelproxy.ParseHost(host, contentDomain); ok {
			return hostRoute{kind: hostFunnel, subdomain: sub, label: label}
		}
	}
	switch host {
	case baseDomain:
		return hostRoute{kind: hostDocs}
	case contentDomain:
		return hostRoute{kind: hostLanding}
	case orgs.ConsoleSubdomain + "." + baseDomain:
		return hostRoute{kind: hostConsole}
	}
	// Serving the console on any other name would put it on the funnel
	// domain too.
	if strings.HasSuffix(host, "."+baseDomain) || (contentDomain != "" && strings.HasSuffix(host, "."+contentDomain)) {
		return hostRoute{kind: hostNotFound}
	}
	return hostRoute{kind: hostConsole}
}
