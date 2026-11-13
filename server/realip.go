package server

import (
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// TrustedProxies returns next with r.RemoteAddr set to the client's address
// when the request comes through trusted proxies. That address is the
// rightmost untrusted one in X-Forwarded-For, since a client can put
// anything to the left of it. The header is always removed, so a client
// can't spoof it to the services behind ownscale.
func TrustedProxies(trusted []netip.Prefix, next http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xff := r.Header.Values("X-Forwarded-For")
		r.Header.Del("X-Forwarded-For")
		if peer, err := netip.ParseAddrPort(r.RemoteAddr); err == nil && isTrusted(peer.Addr().Unmap()) {
			if client, ok := forwardedClient(xff, isTrusted); ok {
				r.RemoteAddr = netip.AddrPortFrom(client, 0).String()
			}
		}
		next.ServeHTTP(w, r)
	})
}

// forwardedClient walks the X-Forwarded-For hops from the right and returns
// the first untrusted one, or the leftmost valid one if all are trusted.
func forwardedClient(xff []string, isTrusted func(netip.Addr) bool) (client netip.Addr, ok bool) {
	hops := strings.Split(strings.Join(xff, ","), ",")
	for _, hop := range slices.Backward(hops) {
		a, err := netip.ParseAddr(strings.TrimSpace(hop))
		if err != nil {
			break
		}
		client, ok = a.Unmap().WithZone(""), true
		if !isTrusted(client) {
			break
		}
	}
	return client, ok
}
