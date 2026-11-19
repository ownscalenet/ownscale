package orgs

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"time"
)

// clientWindow is how long an organization counts against a client's
// config.Config.MaxOrgsPerIP after the client last used it.
const clientWindow = time.Hour

// ErrClientLimit is returned when a client address would use more than
// config.Config.MaxOrgsPerIP different organizations within clientWindow.
var ErrClientLimit = errors.New("too many organizations used from this address")

type clientCtxKey struct{}

// WithClient returns ctx carrying the address of the client making the
// request, which AllowClient limits. An unparsable remoteAddr leaves ctx as is.
func WithClient(ctx context.Context, remoteAddr string) context.Context {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return ctx
	}
	addr := ap.Addr().Unmap()
	if addr.Is6() {
		// A single host can use any address in its /64.
		return context.WithValue(ctx, clientCtxKey{}, netip.PrefixFrom(addr, 64).Masked().String())
	}
	return context.WithValue(ctx, clientCtxKey{}, addr.String())
}

// AllowClient returns ErrClientLimit if the client in ctx (see WithClient)
// would exceed config.Config.MaxOrgsPerIP by using subdomain. Every use
// counts, not only wakes, so a client can't keep orgs awake by touching them
// while waking others. A ctx without a client is never limited. It doesn't
// check that subdomain exists.
func (s *Service) AllowClient(ctx context.Context, subdomain string) error {
	client, ok := ctx.Value(clientCtxKey{}).(string)
	if !ok || s.cfg.MaxOrgsPerIP <= 0 {
		return nil
	}
	if !s.clients.allow(time.Now(), client, subdomain, s.cfg.MaxOrgsPerIP) {
		s.logger.Warn("client uses too many organizations", "client", client, "subdomain", subdomain, "max", s.cfg.MaxOrgsPerIP)
		return ErrClientLimit
	}
	return nil
}

// clientLimiter remembers when each client last used each organization.
type clientLimiter struct {
	mu sync.Mutex
	// used maps a client to its organizations' last use.
	used map[string]map[string]time.Time
	// swept is when all clients were last pruned.
	swept time.Time
}

// allow records that client uses subdomain at now and reports whether that
// keeps it within max organizations in the last clientWindow.
func (l *clientLimiter) allow(now time.Time, client, subdomain string, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Without sweeping, clients that never return would stay forever.
	if now.Sub(l.swept) >= clientWindow {
		for c := range l.used {
			l.prune(now, c)
		}
		l.swept = now
	}
	l.prune(now, client)
	orgs := l.used[client]
	if _, ok := orgs[subdomain]; !ok && len(orgs) >= max {
		return false
	}
	if orgs == nil {
		orgs = make(map[string]time.Time)
		if l.used == nil {
			l.used = make(map[string]map[string]time.Time)
		}
		l.used[client] = orgs
	}
	orgs[subdomain] = now
	return true
}

// prune forgets client's organizations it didn't use within clientWindow.
func (l *clientLimiter) prune(now time.Time, client string) {
	orgs := l.used[client]
	for sub, last := range orgs {
		if now.Sub(last) >= clientWindow {
			delete(orgs, sub)
		}
	}
	if len(orgs) == 0 {
		delete(l.used, client)
	}
}
