// The stress command joins many ephemeral tsnet clients to an organization's
// tailnet and keeps them connected, to load its headscale's control plane.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ownscalenet/ownscale/e2e/connectivity/tscl"
)

const (
	reportInterval  = 5 * time.Second
	shutdownTimeout = 30 * time.Second
)

type config struct {
	subdomain   string
	authKey     string
	baseDomain  string
	n           int
	ramp        time.Duration
	duration    time.Duration
	joinTimeout time.Duration
	verbose     bool
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "stress:", err)
		os.Exit(2)
	}
	// tsnet also logs through the standard logger; -v covers what we need.
	log.SetOutput(io.Discard)
	if os.Getenv("TS_NO_LOGS_NO_SUPPORT") == "" {
		os.Setenv("TS_NO_LOGS_NO_SUPPORT", "true")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "stress:", err)
		os.Exit(1)
	}
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("stress", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: go run ./e2e/stress -subdomain SUB -authkey KEY [flags]

Joins -n ephemeral tsnet clients to https://SUB.BASE_DOMAIN using a reusable
pre-auth key, keeps them connected for -duration, then logs them out.
For the dev stack, trust its CA: SSL_CERT_FILE=$PWD/data/local-ca/root.crt

`)
		fs.PrintDefaults()
	}
	cfg := config{}
	fs.StringVar(&cfg.subdomain, "subdomain", os.Getenv("STRESS_SUBDOMAIN"), "organization subdomain (env STRESS_SUBDOMAIN)")
	fs.StringVar(&cfg.authKey, "authkey", os.Getenv("STRESS_AUTHKEY"), "reusable pre-auth key (env STRESS_AUTHKEY)")
	fs.StringVar(&cfg.baseDomain, "base-domain", cmp.Or(os.Getenv("BASE_DOMAIN"), "ownscale.test"), "base domain (env BASE_DOMAIN)")
	fs.IntVar(&cfg.n, "n", 300, "number of clients")
	fs.DurationVar(&cfg.ramp, "ramp", 200*time.Millisecond, "delay between client starts")
	fs.DurationVar(&cfg.duration, "duration", 5*time.Minute, "how long to run; 0 runs until interrupted")
	fs.DurationVar(&cfg.joinTimeout, "join-timeout", time.Minute, "per-client join timeout")
	fs.BoolVar(&cfg.verbose, "v", false, "print tsnet debug logs to stderr")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	switch {
	case fs.NArg() != 0:
		return config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	case cfg.subdomain == "":
		return config{}, errors.New("-subdomain is required")
	case cfg.authKey == "":
		return config{}, errors.New("-authkey is required")
	case cfg.n < 1:
		return config{}, errors.New("-n must be at least 1")
	case cfg.ramp < 0 || cfg.duration < 0 || cfg.joinTimeout <= 0:
		return config{}, errors.New("-ramp and -duration must not be negative, -join-timeout must be positive")
	}
	return cfg, nil
}

func run(ctx context.Context, cfg config, out io.Writer) error {
	controlURL := fmt.Sprintf("https://%s.%s", cfg.subdomain, cfg.baseDomain)
	runID := strings.ToLower(rand.Text()[:6])
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if cfg.verbose {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	fmt.Fprintf(out, "stress %s: %d clients -> %s\n", runID, cfg.n, controlURL)

	runCtx := ctx
	if cfg.duration > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, cfg.duration)
		defer cancel()
	}

	st := newStats(time.Now())
	stopReport := make(chan struct{})
	reportDone := make(chan struct{})
	go func() {
		defer close(reportDone)
		t := time.NewTicker(reportInterval)
		defer t.Stop()
		for {
			select {
			case now := <-t.C:
				fmt.Fprintln(out, st.summary(now))
			case <-stopReport:
				return
			}
		}
	}()

	var (
		mu      sync.Mutex
		clients []*tscl.Client
		joins   sync.WaitGroup
	)
start:
	for i := range cfg.n {
		if i > 0 {
			select {
			case <-runCtx.Done():
				break start
			case <-time.After(cfg.ramp):
			}
		}
		st.start()
		joins.Go(func() {
			began := time.Now()
			c, err := tscl.New(runCtx, tscl.Config{
				Hostname:       fmt.Sprintf("stress-%s-%d", runID, i),
				ControlURL:     controlURL,
				AuthKey:        cfg.authKey,
				Logger:         logger,
				StartupTimeout: cfg.joinTimeout,
			})
			st.joined(time.Since(began), err)
			if err == nil {
				mu.Lock()
				clients = append(clients, c)
				mu.Unlock()
			}
		})
	}

	<-runCtx.Done()
	joins.Wait()
	close(stopReport)
	<-reportDone
	fmt.Fprintln(out, st.summary(time.Now()))
	st.writeErrors(out)

	fmt.Fprintf(out, "logging out %d clients\n", len(clients))
	return shutdown(clients)
}

// shutdown logs out and closes clients in parallel. It doesn't take the run's
// context, which is already done by now.
func shutdown(clients []*tscl.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, c := range clients {
		wg.Go(func() {
			if err := errors.Join(c.Logout(ctx), c.Close()); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("%d of %d clients failed to clean up, first: %w", len(errs), len(clients), errs[0])
	}
	return nil
}

type stats struct {
	mu        sync.Mutex
	began     time.Time
	started   int
	connected int
	failed    int
	latencies []time.Duration
	errs      map[string]int
}

func newStats(began time.Time) *stats {
	return &stats{began: began, errs: map[string]int{}}
}

func (s *stats) start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started++
}

func (s *stats) joined(d time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.failed++
		s.errs[err.Error()]++
		return
	}
	s.connected++
	s.latencies = append(s.latencies, d)
}

func (s *stats) summary(now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sorted := slices.Sorted(slices.Values(s.latencies))
	return fmt.Sprintf("[%s] started=%d connected=%d failed=%d pending=%d join p50=%s p95=%s max=%s",
		now.Sub(s.began).Round(time.Second), s.started, s.connected, s.failed,
		s.started-s.connected-s.failed,
		percentile(sorted, 50), percentile(sorted, 95), percentile(sorted, 100))
}

func (s *stats) writeErrors(out io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msg := range slices.Sorted(maps.Keys(s.errs)) {
		fmt.Fprintf(out, "  %dx %s\n", s.errs[msg], msg)
	}
}

// percentile returns the nearest-rank p-th percentile of sorted, rounded to
// milliseconds, or 0 if sorted is empty.
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100
	return sorted[max(rank, 1)-1].Round(time.Millisecond)
}
