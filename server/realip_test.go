package server

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestTrustedProxies(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fc00::/7")}
	tests := []struct {
		name       string
		trusted    []netip.Prefix
		remoteAddr string
		xff        []string
		want       string
	}{
		{"no trusted proxies", nil, "10.0.0.1:1234", []string{"1.2.3.4"}, "10.0.0.1:1234"},
		{"untrusted peer", trusted, "5.6.7.8:1234", []string{"1.2.3.4"}, "5.6.7.8:1234"},
		{"trusted peer without header", trusted, "10.0.0.1:1234", nil, "10.0.0.1:1234"},
		{"one proxy", trusted, "10.0.0.1:1234", []string{"1.2.3.4"}, "1.2.3.4:0"},
		{"client spoofs the left", trusted, "10.0.0.1:1234", []string{"9.9.9.9, 1.2.3.4"}, "1.2.3.4:0"},
		{"chained trusted proxies", trusted, "10.0.0.1:1234", []string{"1.2.3.4, 10.0.0.2", "10.0.0.3"}, "1.2.3.4:0"},
		{"all hops trusted", trusted, "10.0.0.1:1234", []string{"10.0.0.2, 10.0.0.3"}, "10.0.0.2:0"},
		{"garbage", trusted, "10.0.0.1:1234", []string{"nonsense"}, "10.0.0.1:1234"},
		{"garbage left of client", trusted, "10.0.0.1:1234", []string{"nonsense, 1.2.3.4"}, "1.2.3.4:0"},
		{"ipv6", trusted, "[fd00::1]:1234", []string{"2001:db8::1"}, "[2001:db8::1]:0"},
		{"ipv4-mapped peer", trusted, "[::ffff:10.0.0.1]:1234", []string{"1.2.3.4"}, "1.2.3.4:0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAddr string
			var gotXFF []string
			h := TrustedProxies(tt.trusted, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAddr, gotXFF = r.RemoteAddr, r.Header.Values("X-Forwarded-For")
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if gotAddr != tt.want {
				t.Errorf("RemoteAddr = %q, want %q", gotAddr, tt.want)
			}
			if len(gotXFF) != 0 {
				t.Errorf("X-Forwarded-For = %q, want it removed", gotXFF)
			}
		})
	}
}
