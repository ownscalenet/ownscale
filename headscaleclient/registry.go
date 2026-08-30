package headscaleclient

import (
	"context"
	"sync"

	"github.com/ownscalenet/ownscale/derrors"
)

// ClientMinter builds a fresh Client for the organization identified by
// subdomain, minting an independent headscale API key and pointing the
// client at that org's headscale instance. Implemented by *dockerctl.Ctl;
// declared here as a narrow interface (rather than importing dockerctl
// directly) so Registry stays decoupled from Docker specifics and easy to
// test with a fake.
type ClientMinter interface {
	MintClient(ctx context.Context, subdomain string) (*Client, error)
}

// Registry lazily mints and caches one Client per organization subdomain,
// entirely in memory: the ownscale-facing headscale API key is never
// persisted anywhere. A process restart simply mints a fresh key next
// time a Client is needed for a given subdomain -- headscale allows any
// number of simultaneously valid API keys, so this never invalidates keys
// already handed out to anything else (e.g. headplane's own key).
type Registry struct {
	minter ClientMinter

	mu      sync.Mutex
	clients map[string]*Client // keyed by organization subdomain
}

// NewRegistry builds a Registry that builds clients via minter.
func NewRegistry(minter ClientMinter) *Registry {
	return &Registry{minter: minter, clients: make(map[string]*Client)}
}

// Client returns the cached Client for subdomain, building (and caching)
// one via the Registry's ClientMinter on first use.
func (r *Registry) Client(ctx context.Context, subdomain string) (_ *Client, err error) {
	defer derrors.Wrap(&err, "r.Client(%q)", subdomain)

	if c, ok := r.cached(subdomain); ok {
		return c, nil
	}

	c, err := r.minter.MintClient(ctx, subdomain)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// A concurrent call might have raced us to mint+cache a client for the
	// same subdomain; keep whichever was cached first rather than
	// overwriting it -- both keys are equally valid, so there's no
	// correctness issue either way, just avoids leaking the loser's Client.
	if existing, ok := r.clients[subdomain]; ok {
		return existing, nil
	}
	r.clients[subdomain] = c
	return c, nil
}

func (r *Registry) cached(subdomain string) (*Client, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.clients[subdomain]
	return c, ok
}
