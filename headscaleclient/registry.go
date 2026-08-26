package headscaleclient

import (
	"context"
	"sync"

	"github.com/ownscalenet/ownscale/derrors"
)

// KeyMinter mints a fresh, independent headscale API key for the
// organization whose headscale container is identified by
// containerNameOrID (see dockerctl.HSContainerName). Implemented by
// *dockerctl.Ctl; declared here as a narrow interface (rather than
// importing dockerctl directly) so Registry stays decoupled from Docker
// specifics and easy to test with a fake.
type KeyMinter interface {
	MintHeadscaleAPIKey(ctx context.Context, containerNameOrID string) (string, error)
}

// Registry lazily mints and caches one Client per organization subdomain,
// entirely in memory: the ownscale-facing headscale API key is never
// persisted anywhere. A process restart simply mints a fresh key next
// time a Client is needed for a given subdomain -- headscale allows any
// number of simultaneously valid API keys, so this never invalidates keys
// already handed out to anything else (e.g. headplane's own key).
type Registry struct {
	minter KeyMinter

	mu      sync.Mutex
	clients map[string]*Client // keyed by organization subdomain
}

// NewRegistry builds a Registry that mints keys via minter.
func NewRegistry(minter KeyMinter) *Registry {
	return &Registry{minter: minter, clients: make(map[string]*Client)}
}

// hsContainerName returns the Docker container name of subdomain's
// headscale instance. It intentionally duplicates
// dockerctl.HSContainerName's "hs-<subdomain>" naming convention (rather
// than importing dockerctl) to keep this package decoupled from Docker
// specifics.
func hsContainerName(subdomain string) string {
	return "hs-" + subdomain
}

// Client returns the cached Client for subdomain, minting (and caching) a
// fresh headscale API key via the Registry's KeyMinter on first use. The
// returned Client talks to http://hs-<subdomain>:8080, reachable because
// the ownscale container itself is joined to subdomain's Docker network
// (see dockerctl.EnsureOwnscaleNetworkForOrg).
func (r *Registry) Client(ctx context.Context, subdomain string) (_ *Client, err error) {
	defer derrors.Wrap(&err, "r.Client(%q)", subdomain)

	if c, ok := r.cached(subdomain); ok {
		return c, nil
	}

	containerName := hsContainerName(subdomain)
	apiKey, err := r.minter.MintHeadscaleAPIKey(ctx, containerName)
	if err != nil {
		return nil, err
	}
	c := New(containerName+":8080", apiKey)

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
