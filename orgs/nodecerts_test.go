package orgs

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
	"github.com/miekg/dns"
	"tailscale.com/tsnet"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/dbtest"
)

const (
	testZone    = "example.com."
	testTSIGKey = "ownscale-test."
)

// TestTailscaleCert runs `tailscale cert` end to end: headscale publishes
// the node's DNS-01 challenge through the real rfc2136 provider into a test
// zone, and an in-process Pebble CA validates it and issues the certificate.
func TestTailscaleCert(t *testing.T) {
	zone := startTestZone(t)
	pebble := startPebble(t, zone.addr)

	_, queries := dbtest.New(t)
	cfg := &config.Config{
		FastOrgs:        true,
		BaseDomain:      "example.com",
		ContentDomain:   "example.com",
		DataDir:         t.TempDir(),
		FixedSubdomains: testFixedSubdomains,
		DNS: config.DNSConfig{
			Provider: "rfc2136",
			Zone:     "example.com",
			RFC2136: config.RFC2136DNSConfig{
				Server:  zone.addr,
				KeyName: testTSIGKey,
				KeyAlg:  "hmac-sha256",
				Key:     zone.secret,
			},
		},
	}
	svc, err := NewService(queries, cfg, "https://auth.example.com", slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.derpMap.Store(testDERPMap(1))
	org := createStarted(t, svc, "acme")
	client, err := svc.Client("acme")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	port, _ := svc.LookupPort("acme")

	// tailscaled's ACME client uses http.DefaultClient and Let's Encrypt
	// unless told otherwise.
	t.Setenv("TS_DEBUG_ACME_DIRECTORY_URL", pebble.directory)
	prev := http.DefaultClient.Transport
	http.DefaultClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pebble.serverRoots}}
	t.Cleanup(func() { http.DefaultClient.Transport = prev })

	srv := &tsnet.Server{
		Dir:        t.TempDir(),
		ControlURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		AuthKey:    newAuthKey(t, client),
		Hostname:   "web",
		Ephemeral:  true,
		Logf:       func(string, ...any) {},
		UserLogf:   func(string, ...any) {},
	}
	t.Cleanup(func() { _ = srv.Close() })
	ctx := t.Context()
	if _, err := srv.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	lc, err := srv.LocalClient()
	if err != nil {
		t.Fatal(err)
	}

	domain := "web." + svc.tailnetDomain(org.TailnetLabel)
	certPEM, _, err := lc.CertPair(ctx, domain)
	if err != nil {
		t.Fatalf("CertPair(%q): %v", domain, err)
	}
	var chain []*x509.Certificate
	for block, rest := pem.Decode(certPEM); block != nil; block, rest = pem.Decode(rest) {
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, c)
	}
	if len(chain) == 0 {
		t.Fatal("no PEM certificate returned")
	}
	leaf, intermediates := chain[0], x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: domain, Roots: pebble.issuerRoots, Intermediates: intermediates}); err != nil {
		t.Errorf("certificate does not verify for %q: %v", domain, err)
	}
}

// testZoneServer is an authoritative DNS server for testZone that accepts
// TSIG-signed RFC 2136 updates of TXT records.
type testZoneServer struct {
	addr   string
	secret string

	mu  sync.Mutex
	txt map[string][]string
}

func startTestZone(t *testing.T) *testZoneServer {
	t.Helper()
	z := &testZoneServer{
		secret: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		txt:    map[string][]string{},
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	z.addr = pc.LocalAddr().String()
	secrets := map[string]string{testTSIGKey: z.secret}
	for _, srv := range []*dns.Server{
		{PacketConn: pc, Handler: z, TsigSecret: secrets, MsgAcceptFunc: acceptUpdates},
		{Listener: ln, Handler: z, TsigSecret: secrets, MsgAcceptFunc: acceptUpdates},
	} {
		go func() { _ = srv.ActivateAndServe() }()
		t.Cleanup(func() { _ = srv.Shutdown() })
	}
	return z
}

// acceptUpdates also lets through UPDATE, which dns.DefaultMsgAcceptFunc
// rejects.
func acceptUpdates(dh dns.Header) dns.MsgAcceptAction {
	if int(dh.Bits>>11)&0xF == dns.OpcodeUpdate {
		return dns.MsgAccept
	}
	return dns.DefaultMsgAcceptFunc(dh)
}

func (z *testZoneServer) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	if r.Opcode == dns.OpcodeUpdate {
		z.update(w, r, m)
		return
	}
	q := r.Question[0]
	name := strings.ToLower(q.Name)
	hdr := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Rrtype: q.Qtype, Ttl: 60}
	switch {
	case !dns.IsSubDomain(testZone, name):
		m.Rcode = dns.RcodeRefused
	case q.Qtype == dns.TypeSOA && name == testZone:
		m.Answer = []dns.RR{&dns.SOA{
			Hdr: hdr, Ns: "ns." + testZone, Mbox: "hostmaster." + testZone,
			Serial: 1, Refresh: 60, Retry: 60, Expire: 60, Minttl: 60,
		}}
	case q.Qtype == dns.TypeTXT:
		z.mu.Lock()
		for _, v := range z.txt[name] {
			m.Answer = append(m.Answer, &dns.TXT{Hdr: hdr, Txt: []string{v}})
		}
		z.mu.Unlock()
	}
	_ = w.WriteMsg(m)
}

func (z *testZoneServer) update(w dns.ResponseWriter, r *dns.Msg, m *dns.Msg) {
	tsig := r.IsTsig()
	if tsig == nil || w.TsigStatus() != nil {
		m.Rcode = dns.RcodeNotAuth
		_ = w.WriteMsg(m)
		return
	}
	z.mu.Lock()
	for _, rr := range r.Ns {
		txt, ok := rr.(*dns.TXT)
		if !ok {
			continue
		}
		name := strings.ToLower(txt.Hdr.Name)
		value := strings.Join(txt.Txt, "")
		if txt.Hdr.Class == dns.ClassINET {
			z.txt[name] = append(z.txt[name], value)
			continue
		}
		var kept []string
		for _, v := range z.txt[name] {
			if v != value {
				kept = append(kept, v)
			}
		}
		z.txt[name] = kept
	}
	z.mu.Unlock()
	m.SetTsig(tsig.Hdr.Name, tsig.Algorithm, 300, time.Now().Unix())
	_ = w.WriteMsg(m)
}

type testPebble struct {
	directory   string
	serverRoots *x509.CertPool // trusts the ACME server
	issuerRoots *x509.CertPool // trusts issued certificates
}

// startPebble starts an in-process Pebble CA that validates DNS-01
// challenges through resolver.
func startPebble(t *testing.T, resolver string) testPebble {
	t.Helper()
	t.Setenv("PEBBLE_VA_NOSLEEP", "1")
	t.Setenv("PEBBLE_WFE_NONCEREJECT", "0")
	t.Setenv("PEBBLE_AUTHZREUSE", "0")

	logger := log.New(io.Discard, "", 0)
	store := db.NewMemoryStore()
	authority := ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{
		"default": {Description: "default"},
	})
	validator := va.New(logger, 0, 0, false, resolver, store)
	acme := wfe.New(logger, store, validator, authority, []string{"pebble.letsencrypt.org"}, false, false, 0, 0)
	var srv *httptest.Server
	handler := acme.Handler()
	// Pebble's finalize omits Location, which x/crypto/acme needs to poll
	// an order that is still processing.
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := strings.CutPrefix(r.URL.Path, "/finalize-order/"); ok {
			w.Header().Set("Location", srv.URL+"/my-order/"+id)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	p := testPebble{
		directory:   srv.URL + wfe.DirectoryPath,
		serverRoots: x509.NewCertPool(),
		issuerRoots: x509.NewCertPool(),
	}
	p.serverRoots.AddCert(srv.Certificate())
	p.issuerRoots.AddCert(authority.GetRootCert(0).Cert)
	return p
}
