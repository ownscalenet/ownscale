package orgs

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// TestSleepAndWake verifies that sleeping an org stops its headscale and
// closes its connections, and that waking it, by API or device traffic,
// starts it again on the same port.
func TestSleepAndWake(t *testing.T) {
	tests := []struct {
		name string
		wake func(t *testing.T, svc *Service)
	}{
		{
			name: "Wake",
			wake: func(t *testing.T, svc *Service) {
				if _, err := svc.Wake(t.Context(), "acme"); err != nil {
					t.Fatalf("Wake: %v", err)
				}
			},
		},
		{
			name: "DialOrg",
			wake: func(t *testing.T, svc *Service) {
				conn, err := svc.DialOrg(t.Context(), "acme")
				if err != nil {
					t.Fatalf("DialOrg: %v", err)
				}
				conn.Close()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newRealService(t)
			var slept []string
			svc.OnSleep(func(subdomain string) { slept = append(slept, subdomain) })
			org := createStarted(t, svc, "acme")
			port, _ := svc.LookupPort("acme")

			conn, err := svc.DialOrg(t.Context(), "acme")
			if err != nil {
				t.Fatalf("DialOrg: %v", err)
			}
			defer conn.Close()

			orgSvc := svc.ForOrg(org)
			if err := orgSvc.Sleep(t.Context()); err != nil {
				t.Fatalf("Sleep: %v", err)
			}
			// Sleeping a sleeping org is a no-op.
			if err := orgSvc.Sleep(t.Context()); err != nil {
				t.Fatalf("second Sleep: %v", err)
			}
			if got := svc.Status("acme"); got != StatusSleeping {
				t.Errorf("Status after Sleep = %q, want %q", got, StatusSleeping)
			}
			if len(slept) != 1 || slept[0] != "acme" {
				t.Errorf("OnSleep calls = %v, want [acme]", slept)
			}
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
				t.Errorf("connection after Sleep: Read error = %v, want net.ErrClosed", err)
			}

			tt.wake(t, svc)

			if got := svc.Status("acme"); got != StatusActive {
				t.Errorf("Status after waking = %q, want %q", got, StatusActive)
			}
			if got, _ := svc.LookupPort("acme"); got != port {
				t.Errorf("port after waking = %d, want %d", got, port)
			}
			client, err := svc.Client("acme")
			if err != nil {
				t.Fatalf("Client: %v", err)
			}
			if _, err := client.ListUsers(t.Context()); err != nil {
				t.Errorf("ListUsers after waking: %v", err)
			}
		})
	}
}

func TestWakeUnprovisioned(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Wake(t.Context(), "acme"); !errors.Is(err, ErrNotProvisioned) {
		t.Errorf("Wake error = %v, want ErrNotProvisioned", err)
	}
	if got := svc.Status("acme"); got != StatusUnprovisioned {
		t.Errorf("Status = %q, want %q", got, StatusUnprovisioned)
	}
}

// TestSleepIdle verifies that only running orgs without open connections
// that went unused for idleTimeout are put to sleep.
func TestSleepIdle(t *testing.T) {
	tests := []struct {
		name string
		// setup makes acme's runtime in svc.
		setup     func(t *testing.T, svc *Service)
		wantSleep bool
	}{
		{
			name: "idle",
			setup: func(t *testing.T, svc *Service) {
				rt := serving(svc, "acme", 1, false)
				rt.lastActivity.Store(time.Now().Add(-2 * time.Hour).UnixNano())
			},
			wantSleep: true,
		},
		{
			name:  "recently used",
			setup: func(t *testing.T, svc *Service) { serving(svc, "acme", 1, false) },
		},
		{
			name: "open connection",
			setup: func(t *testing.T, svc *Service) {
				rt := serving(svc, "acme", 1, false)
				c1, c2 := net.Pipe()
				t.Cleanup(func() { c1.Close(); c2.Close() })
				rt.track(c1)
				rt.lastActivity.Store(time.Now().Add(-2 * time.Hour).UnixNano())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			svc.idleTimeout = time.Hour
			var slept bool
			svc.OnSleep(func(string) { slept = true })
			if _, err := svc.Create(t.Context(), "acme"); err != nil {
				t.Fatalf("Create: %v", err)
			}
			tt.setup(t, svc)

			svc.sleepIdle(t.Context())

			if slept != tt.wantSleep {
				t.Errorf("slept = %v, want %v", slept, tt.wantSleep)
			}
			if _, running := svc.running("acme"); running == tt.wantSleep {
				t.Errorf("running = %v, want %v", running, !tt.wantSleep)
			}
		})
	}
}

// TestRunSleeper verifies the sleeper goroutine puts idle orgs to sleep
// and stops with its context.
func TestRunSleeper(t *testing.T) {
	svc := newTestService(t)
	svc.idleTimeout = 0
	svc.sweepInterval = 10 * time.Millisecond
	if _, err := svc.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	serving(svc, "acme", 1, false)
	slept := make(chan struct{}, 1)
	svc.OnSleep(func(string) { slept <- struct{}{} })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RunSleeper(ctx)
	}()
	select {
	case <-slept:
	case <-time.After(5 * time.Second):
		t.Fatal("org not put to sleep")
	}
	if got := svc.Status("acme"); got != StatusSleeping {
		t.Errorf("Status = %q, want %q", got, StatusSleeping)
	}
	cancel()
	<-done
}

// TestWakeIgnoresCanceledRequest verifies that a canceled request doesn't
// abort a start, which would leave the org down for startRetryDelay.
func TestWakeIgnoresCanceledRequest(t *testing.T) {
	svc := newRealService(t)
	org := createStarted(t, svc, "acme")
	if err := svc.ForOrg(org).Sleep(t.Context()); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.Wake(ctx, "acme"); err != nil {
		t.Fatalf("Wake with canceled ctx: %v", err)
	}
	if got := svc.Status("acme"); got != StatusActive {
		t.Errorf("Status = %q, want %q", got, StatusActive)
	}
}
