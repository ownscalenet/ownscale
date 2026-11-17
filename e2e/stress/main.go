// The stress command joins many tsnet clients to several organizations'
// tailnets and keeps them connected, to load their headscales' control planes.
// Organizations and machines are cached in a state dir and reused by later runs.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"maps"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ownscalenet/ownscale/e2e/connectivity/tscl"
	"tailscale.com/envknob"
)

const (
	reportInterval = 5 * time.Second
)

type config struct {
	baseDomain  string
	orgs        int
	n           int
	stateDir    string
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
	// tsnet also logs through the standard logger, which -v keeps.
	if !cfg.verbose {
		log.SetOutput(io.Discard)
	}
	if os.Getenv("TS_NO_LOGS_NO_SUPPORT") == "" {
		os.Setenv("TS_NO_LOGS_NO_SUPPORT", "true")
	}
	// Otherwise every client maps a port on the local router over UPnP, and
	// on close the router can't keep up with removing them all, stalling
	// tsnet past its deadlock watchdog. The knob is read at init, so only
	// envknob.Setenv changes it now.
	envknob.Setenv("TS_DISABLE_PORTMAPPER", "true")
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
		fmt.Fprint(fs.Output(), `Usage: go run ./e2e/stress -orgs N -n M [flags]

Signs up -orgs organizations on BASE_DOMAIN with passkeys and joins -n tsnet
clients to each, keeping them connected for -duration. Accounts, auth keys and
machine state are cached in -state, so later runs reuse the same orgs and nodes.
For the dev stack, trust its CA: SSL_CERT_FILE=$PWD/data/local-ca/root.crt

`)
		fs.PrintDefaults()
	}
	cfg := config{}
	fs.StringVar(&cfg.baseDomain, "base-domain", cmp.Or(os.Getenv("BASE_DOMAIN"), "ownscale.test"), "base domain (env BASE_DOMAIN)")
	fs.IntVar(&cfg.orgs, "orgs", 1, "number of organizations")
	fs.IntVar(&cfg.n, "n", 10, "number of clients per organization")
	fs.StringVar(&cfg.stateDir, "state", "", "cache dir (default bin/stress/BASE_DOMAIN)")
	fs.DurationVar(&cfg.ramp, "ramp", 1000*time.Millisecond, "delay between client starts")
	fs.DurationVar(&cfg.duration, "duration", 5*time.Minute, "how long to run; 0 runs until interrupted")
	fs.DurationVar(&cfg.joinTimeout, "join-timeout", time.Minute, "per-client join timeout")
	fs.BoolVar(&cfg.verbose, "v", false, "print tsnet debug logs to stderr")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	switch {
	case fs.NArg() != 0:
		return config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	case cfg.orgs < 1 || cfg.n < 1:
		return config{}, errors.New("-orgs and -n must be at least 1")
	case cfg.ramp < 0 || cfg.duration < 0 || cfg.joinTimeout <= 0:
		return config{}, errors.New("-ramp and -duration must not be negative, -join-timeout must be positive")
	}
	cfg.stateDir = cmp.Or(cfg.stateDir, filepath.Join("bin", "stress", cfg.baseDomain))
	return cfg, nil
}

// target is one machine to join.
type target struct {
	org      *org
	hostname string
	stateDir string
}

// targets lists n machines for each org, interleaved so every org ramps up
// at the same pace. Hostnames and state dirs are the same on every run, so
// each machine rejoins as the node it was before.
func targets(orgs []*org, n int, stateDir string) []target {
	var ts []target
	for j := range n {
		for _, o := range orgs {
			ts = append(ts, target{
				org:      o,
				hostname: fmt.Sprintf("stress-%d", j),
				stateDir: filepath.Join(stateDir, o.subdomain, fmt.Sprintf("m%d", j)),
			})
		}
	}
	return ts
}

func run(ctx context.Context, cfg config, out io.Writer) error {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if cfg.verbose {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	sf, err := loadState(cfg.stateDir)
	if err != nil {
		return err
	}
	orgs, err := provisionOrgs(ctx, cfg, sf, out)
	if err != nil {
		return err
	}
	if err := checkResolves(ctx, orgs, cfg.baseDomain); err != nil {
		return err
	}
	ts := targets(orgs, cfg.n, cfg.stateDir)
	fmt.Fprintf(out, "stress: %d orgs x %d clients on %s\n", cfg.orgs, cfg.n, cfg.baseDomain)

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
	for i, t := range ts {
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
				Hostname:       t.hostname,
				ControlURL:     fmt.Sprintf("https://%s.%s", t.org.subdomain, cfg.baseDomain),
				AuthKey:        t.org.authKey,
				Logger:         logger,
				StartupTimeout: cfg.joinTimeout,
				StateDir:       t.stateDir,
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

	fmt.Fprintf(out, "closing %d clients\n", len(clients))
	err = shutdown(clients)
	return errors.Join(err, reportMachines(ctx, orgs, out))
}

// checkResolves fails early when an org's name doesn't resolve, as happens on
// the dev stack, which has no wildcard DNS; joins would only time out.
func checkResolves(ctx context.Context, orgs []*org, baseDomain string) error {
	var missing []string
	for _, o := range orgs {
		host := o.subdomain + "." + baseDomain
		if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil {
			missing = append(missing, host)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("these orgs don't resolve, add them to /etc/hosts:\n127.0.0.1 %s", strings.Join(missing, " "))
	}
	return nil
}

// reportMachines prints how many machines each org has, which should stay at
// -n across runs since machines rejoin as the same nodes.
func reportMachines(ctx context.Context, orgs []*org, out io.Writer) error {
	// ctx is done if the run was interrupted, but the report still matters.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), apiTimeout)
	defer cancel()
	for _, o := range orgs {
		n, err := o.console.machineCount(ctx)
		if err != nil {
			return fmt.Errorf("org %d: count machines: %w", o.index, err)
		}
		fmt.Fprintf(out, "org %d %s: %d machines\n", o.index, o.subdomain, n)
	}
	return nil
}

// shutdown closes clients in parallel without logging them out: logging out
// would expire the nodes, which later runs reuse.
func shutdown(clients []*tscl.Client) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, c := range clients {
		wg.Go(func() {
			if err := c.Close(); err != nil {
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
