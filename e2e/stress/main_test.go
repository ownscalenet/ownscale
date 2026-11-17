package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	vwa "github.com/descope/virtualwebauthn"
)

func TestPercentile(t *testing.T) {
	ms := func(vs ...int) []time.Duration {
		var ds []time.Duration
		for _, v := range vs {
			ds = append(ds, time.Duration(v)*time.Millisecond)
		}
		return ds
	}
	for _, tt := range []struct {
		name   string
		sorted []time.Duration
		p      int
		want   time.Duration
	}{
		{"empty", nil, 50, 0},
		{"single p50", ms(7), 50, 7 * time.Millisecond},
		{"single p0", ms(7), 0, 7 * time.Millisecond},
		{"p50 of 4", ms(1, 2, 3, 4), 50, 2 * time.Millisecond},
		{"p95 of 4", ms(1, 2, 3, 4), 95, 4 * time.Millisecond},
		{"p95 of 100", ms(seq(1, 100)...), 95, 95 * time.Millisecond},
		{"max", ms(1, 5, 9), 100, 9 * time.Millisecond},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := percentile(tt.sorted, tt.p); got != tt.want {
				t.Fatalf("percentile(%d) = %s, want %s", tt.p, got, tt.want)
			}
		})
	}
}

func seq(from, to int) []int {
	var s []int
	for i := from; i <= to; i++ {
		s = append(s, i)
	}
	return s
}

func TestStatsSummary(t *testing.T) {
	began := time.Now()
	s := newStats(began)
	for range 4 {
		s.start()
	}
	s.joined(300*time.Millisecond, nil)
	s.joined(100*time.Millisecond, nil)
	s.joined(time.Second, errors.New("boom"))

	want := "[10s] started=4 connected=2 failed=1 pending=1 join p50=100ms p95=300ms max=300ms"
	if got := s.summary(began.Add(10 * time.Second)); got != want {
		t.Fatalf("summary() = %q, want %q", got, want)
	}
	var b strings.Builder
	s.writeErrors(&b)
	if got := b.String(); got != "  1x boom\n" {
		t.Fatalf("writeErrors() = %q", got)
	}
}

func TestParseFlags(t *testing.T) {
	t.Setenv("BASE_DOMAIN", "")
	for _, tt := range []struct {
		name      string
		args      []string
		wantOrgs  int
		wantN     int
		wantState string
		valid     bool
	}{
		{"defaults", nil, 1, 10, "bin/stress/ownscale.test", true},
		{"orgs x machines", []string{"-orgs", "10", "-n", "10"}, 10, 10, "bin/stress/ownscale.test", true},
		{"remote", []string{"-base-domain", "example.com"}, 1, 10, "bin/stress/example.com", true},
		{"state dir", []string{"-state", "/tmp/x"}, 1, 10, "/tmp/x", true},
		{"until interrupted", []string{"-duration", "0"}, 1, 10, "bin/stress/ownscale.test", true},
		{"zero orgs", []string{"-orgs", "0"}, 0, 0, "", false},
		{"zero clients", []string{"-n", "0"}, 0, 0, "", false},
		{"negative ramp", []string{"-ramp", "-1s"}, 0, 0, "", false},
		{"zero join timeout", []string{"-join-timeout", "0"}, 0, 0, "", false},
		{"extra args", []string{"extra"}, 0, 0, "", false},
		{"removed flag", []string{"-subdomain", "aaaa"}, 0, 0, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseFlags(tt.args)
			if (err == nil) != tt.valid {
				t.Fatalf("parseFlags() = %v, valid = %v", err, tt.valid)
			}
			if !tt.valid {
				return
			}
			if cfg.orgs != tt.wantOrgs || cfg.n != tt.wantN || cfg.stateDir != tt.wantState {
				t.Fatalf("parseFlags() = orgs %d, n %d, state %q; want %d, %d, %q",
					cfg.orgs, cfg.n, cfg.stateDir, tt.wantOrgs, tt.wantN, tt.wantState)
			}
		})
	}
}

func TestTargets(t *testing.T) {
	orgs := []*org{{index: 0, subdomain: "aaaa"}, {index: 1, subdomain: "bbbb"}}
	got := targets(orgs, 2, "state")
	want := []struct{ sub, host, dir string }{
		{"aaaa", "stress-0", "state/aaaa/m0"},
		{"bbbb", "stress-0", "state/bbbb/m0"},
		{"aaaa", "stress-1", "state/aaaa/m1"},
		{"bbbb", "stress-1", "state/bbbb/m1"},
	}
	if len(got) != len(want) {
		t.Fatalf("len(targets) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if g := got[i]; g.org.subdomain != w.sub || g.hostname != w.host || g.stateDir != w.dir {
			t.Errorf("targets[%d] = %s %s %s, want %s %s %s", i, g.org.subdomain, g.hostname, g.stateDir, w.sub, w.host, w.dir)
		}
	}
}

// TestStateRoundTrip checks that a cached passkey still works after a reload,
// since that's what lets later runs sign back in to the same org.
func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sf, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	auth := vwa.NewAuthenticator()
	auth.Options.UserHandle = []byte("user-1")
	auth.AddCredential(vwa.NewCredential(vwa.KeyTypeEC2))
	st := sf.entry(1)
	*st = orgState{
		Username: "stress-abc", Authenticator: auth, Subdomain: "abcd",
		Cookies: []savedCookie{{Name: "s", Value: "v"}}, AuthKeyID: "7", AuthKey: "hskey",
	}
	if err := sf.save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.orgs) != 2 {
		t.Fatalf("len(orgs) = %d, want 2", len(loaded.orgs))
	}
	want, _ := json.Marshal(st)
	got, _ := json.Marshal(loaded.orgs[1])
	if string(got) != string(want) {
		t.Fatalf("loaded org = %s, want %s", got, want)
	}

	// The public key is derived from the cached private key, so a match
	// means the reloaded passkey signs as the one registered.
	la := loaded.orgs[1].Authenticator
	if !bytes.Equal(la.Credentials[0].Key.AttestationData(), auth.Credentials[0].Key.AttestationData()) {
		t.Fatal("reloaded passkey has a different public key")
	}
}
