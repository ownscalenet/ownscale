package main

import (
	"errors"
	"strings"
	"testing"
	"time"
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
	t.Setenv("STRESS_SUBDOMAIN", "")
	t.Setenv("STRESS_AUTHKEY", "")
	t.Setenv("BASE_DOMAIN", "")
	for _, tt := range []struct {
		name  string
		args  []string
		valid bool
	}{
		{"minimal", []string{"-subdomain", "aaaa", "-authkey", "k"}, true},
		{"until interrupted", []string{"-subdomain", "aaaa", "-authkey", "k", "-duration", "0"}, true},
		{"no subdomain", []string{"-authkey", "k"}, false},
		{"no key", []string{"-subdomain", "aaaa"}, false},
		{"zero clients", []string{"-subdomain", "aaaa", "-authkey", "k", "-n", "0"}, false},
		{"negative ramp", []string{"-subdomain", "aaaa", "-authkey", "k", "-ramp", "-1s"}, false},
		{"zero join timeout", []string{"-subdomain", "aaaa", "-authkey", "k", "-join-timeout", "0"}, false},
		{"extra args", []string{"-subdomain", "aaaa", "-authkey", "k", "extra"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseFlags(tt.args)
			if (err == nil) != tt.valid {
				t.Fatalf("parseFlags() = %v, valid = %v", err, tt.valid)
			}
			if tt.valid && cfg.baseDomain != "ownscale.test" {
				t.Fatalf("baseDomain = %q", cfg.baseDomain)
			}
		})
	}
}

func TestParseFlagsEnv(t *testing.T) {
	t.Setenv("STRESS_SUBDOMAIN", "bbbb")
	t.Setenv("STRESS_AUTHKEY", "secret")
	t.Setenv("BASE_DOMAIN", "example.com")
	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.subdomain != "bbbb" || cfg.authKey != "secret" || cfg.baseDomain != "example.com" {
		t.Fatalf("parseFlags() = %+v", cfg)
	}
}
