package orgs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClientLimiter(t *testing.T) {
	type use struct {
		after  time.Duration // since the start of the test
		client string
		sub    string
		want   bool
	}
	tests := []struct {
		name string
		max  int
		uses []use
	}{
		{
			name: "under the cap",
			max:  2,
			uses: []use{{0, "c", "a", true}, {0, "c", "b", true}},
		},
		{
			name: "over the cap",
			max:  2,
			uses: []use{{0, "c", "a", true}, {0, "c", "b", true}, {0, "c", "x", false}},
		},
		{
			name: "same org again",
			max:  1,
			uses: []use{{0, "c", "a", true}, {time.Minute, "c", "a", true}, {2 * time.Minute, "c", "b", false}},
		},
		{
			name: "clients are separate",
			max:  1,
			uses: []use{{0, "c1", "a", true}, {0, "c2", "b", true}, {0, "c1", "b", false}},
		},
		{
			name: "org expires after the window",
			max:  1,
			uses: []use{{0, "c", "a", true}, {clientWindow - time.Second, "c", "b", false}, {clientWindow, "c", "b", true}},
		},
		{
			name: "using an org renews it",
			max:  1,
			uses: []use{{0, "c", "a", true}, {clientWindow - time.Second, "c", "a", true}, {clientWindow, "c", "b", false}},
		},
		{
			name: "denied use isn't recorded",
			max:  1,
			uses: []use{{0, "c", "a", true}, {time.Minute, "c", "b", false}, {clientWindow, "c", "b", true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l clientLimiter
			start := time.Now()
			for i, u := range tt.uses {
				if got := l.allow(start.Add(u.after), u.client, u.sub, tt.max); got != u.want {
					t.Errorf("use %d (%s uses %s after %v): allowed = %v, want %v", i, u.client, u.sub, u.after, got, u.want)
				}
			}
		})
	}
}

func TestClientLimiterForgetsIdleClients(t *testing.T) {
	var l clientLimiter
	start := time.Now()
	l.allow(start, "gone", "a", 1)
	l.allow(start.Add(clientWindow), "other", "a", 1)
	if _, ok := l.used["gone"]; ok {
		t.Error("client that didn't return is still remembered")
	}
}

func TestAllowClient(t *testing.T) {
	tests := []struct {
		name       string
		max        int
		remoteAddr string // empty: no client in ctx
		subs       []string
		wantErr    bool
	}{
		{name: "within the cap", max: 2, remoteAddr: "192.0.2.1:0", subs: []string{"aaaa", "bbbb"}},
		{name: "over the cap", max: 2, remoteAddr: "192.0.2.1:0", subs: []string{"aaaa", "bbbb", "cccc"}, wantErr: true},
		{name: "cap off", max: 0, remoteAddr: "192.0.2.1:0", subs: []string{"aaaa", "bbbb", "cccc"}},
		{name: "no client", max: 1, subs: []string{"aaaa", "bbbb"}},
		{name: "unparsable address", max: 1, remoteAddr: "not an address", subs: []string{"aaaa", "bbbb"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			svc.cfg.MaxOrgsPerIP = tt.max
			ctx := context.Background()
			if tt.remoteAddr != "" {
				ctx = WithClient(ctx, tt.remoteAddr)
			}
			var err error
			for _, sub := range tt.subs {
				err = svc.AllowClient(ctx, sub)
			}
			if got := errors.Is(err, ErrClientLimit); got != tt.wantErr {
				t.Errorf("last err = %v, want ErrClientLimit: %v", err, tt.wantErr)
			}
		})
	}

	t.Run("ipv6 addresses of one /64 share the cap", func(t *testing.T) {
		svc := newTestService(t)
		svc.cfg.MaxOrgsPerIP = 1
		if err := svc.AllowClient(WithClient(t.Context(), "[2001:db8::1]:0"), "aaaa"); err != nil {
			t.Fatal(err)
		}
		if err := svc.AllowClient(WithClient(t.Context(), "[2001:db8::2]:0"), "bbbb"); !errors.Is(err, ErrClientLimit) {
			t.Errorf("err = %v, want ErrClientLimit", err)
		}
		if err := svc.AllowClient(WithClient(t.Context(), "[2001:db9::1]:0"), "bbbb"); err != nil {
			t.Errorf("other /64: err = %v", err)
		}
	})
}

// TestClientLimitOnWake verifies that waking organizations and creating them
// both count against the client's cap.
func TestClientLimitOnWake(t *testing.T) {
	svc := newTestService(t)
	for _, sub := range []string{"aaaa", "bbbb"} {
		createStarted(t, svc, sub)
		if err := svc.sleepHeadscale(t.Context(), sub); err != nil {
			t.Fatal(err)
		}
	}
	svc.cfg.MaxOrgsPerIP = 1
	ctx := WithClient(t.Context(), "192.0.2.1:0")

	if _, err := svc.Ensure(ctx, "aaaa"); err != nil && !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Ensure(aaaa): %v", err)
	}
	if _, err := svc.Ensure(ctx, "bbbb"); !errors.Is(err, ErrClientLimit) {
		t.Errorf("Ensure(bbbb) = %v, want ErrClientLimit", err)
	}
	if _, err := svc.Create(ctx, "cccc"); !errors.Is(err, ErrClientLimit) {
		t.Errorf("Create(cccc) = %v, want ErrClientLimit", err)
	}
	if _, err := svc.Ensure(t.Context(), "bbbb"); errors.Is(err, ErrClientLimit) {
		t.Errorf("Ensure(bbbb) without a client = %v", err)
	}
}
