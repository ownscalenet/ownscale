package orgs

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

const (
	defaultQueueCapacity    = 4
	defaultRetryBackoff     = 5 * time.Second
	provisionTimeout        = 2 * time.Minute
	maxSubdomainAttempts    = 5
	defaultFailureThreshold = 3
	defaultLongBackoff      = 10 * time.Minute
)

// DefaultSeedSubdomains is a predictable list of orgs.
var DefaultSeedSubdomains = []string{"aaaa", "bbbb", "cccc"}

// Provisioner keeps a small pool of fully-provisioned, not-yet-assigned
// organizations ready to hand out.
type Provisioner struct {
	svc              *Service
	logger           *slog.Logger
	retryBackoff     time.Duration
	failureThreshold int
	longBackoff      time.Duration
	ready            chan sqlcgen.Organization
}

func NewProvisioner(svc *Service, logger *slog.Logger, retryBackoff time.Duration, queueCapacity int) *Provisioner {
	if retryBackoff <= 0 {
		retryBackoff = defaultRetryBackoff
	}
	if queueCapacity <= 0 {
		queueCapacity = defaultQueueCapacity
	}
	return &Provisioner{
		svc:              svc,
		logger:           logger,
		retryBackoff:     retryBackoff,
		failureThreshold: defaultFailureThreshold,
		longBackoff:      defaultLongBackoff,
		ready:            make(chan sqlcgen.Organization, queueCapacity),
	}
}

// Take removes a fully-provisioned organization from the pool, blocking
// until one is available or ctx is done (in which case it returns
// ctx.Err()).
func (p *Provisioner) Take(ctx context.Context) (sqlcgen.Organization, error) {
	select {
	case org := <-p.ready:
		return org, nil
	case <-ctx.Done():
		return sqlcgen.Organization{}, ctx.Err()
	}
}

// Run recovers any organizations left "pending" from a previous restart,
// then replenishes the pool forever.
func (p *Provisioner) Run(ctx context.Context) error {
	if err := p.seedFixed(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if err := p.recoverPending(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	var consecutiveFailures int
	for {
		if err := p.replenishOne(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			consecutiveFailures++
			p.logger.Error("replenish organization pool", "error", err, "consecutive_failures", consecutiveFailures)

			backoff := p.retryBackoff
			if consecutiveFailures >= p.failureThreshold {
				backoff = p.longBackoff
				p.logger.Error("provisioner circuit breaker tripped; backing off",
					"consecutive_failures", consecutiveFailures, "backoff", backoff)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
		} else if consecutiveFailures > 0 {
			p.logger.Info("provisioner recovered after consecutive failures; resuming normal cadence",
				"consecutive_failures", consecutiveFailures)
			consecutiveFailures = 0
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// seedFixed makes sure every subdomain in DefaultSeedSubdomains has an
// organization record, creating it (as "pending", for recoverPending to
// pick up) if missing. Safe to call on every restart: existing
// organizations (in any status) are left untouched.
func (p *Provisioner) seedFixed(ctx context.Context) error {
	for _, subdomain := range DefaultSeedSubdomains {
		_, err := p.svc.queries.GetOrganizationBySubdomain(ctx, subdomain)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := p.svc.CreatePending(ctx, subdomain); err != nil {
			return err
		}
	}
	return nil
}

// recoverPending provisions every organization left "pending" in the database.
func (p *Provisioner) recoverPending(ctx context.Context) error {
	pending, err := p.svc.queries.ListPendingOrganizations(ctx)
	if err != nil {
		return err
	}
	for _, org := range pending {
		provisioned, err := p.provisionOne(ctx, org.ID)
		if err != nil {
			p.logger.Error("recover pending organization", "organization_id", org.ID, "error", err)
			continue
		}
		if err := p.push(ctx, provisioned); err != nil {
			return err
		}
	}
	return nil
}

// replenishOne generates a new organization (retrying its random subdomain
// on collision), provisions it, and pushes it onto the pool, blocking if
// the pool is already full.
func (p *Provisioner) replenishOne(ctx context.Context) error {
	org, err := p.createPendingWithRandomSubdomain(ctx)
	if err != nil {
		return err
	}
	provisioned, err := p.provisionOne(ctx, org.ID)
	if err != nil {
		return err
	}
	return p.push(ctx, provisioned)
}

// createPendingWithRandomSubdomain creates a pending organization under a
// randomly generated subdomain, retrying with a fresh candidate up to
// maxSubdomainAttempts times if one happens to collide with an existing
// organization.
func (p *Provisioner) createPendingWithRandomSubdomain(ctx context.Context) (sqlcgen.Organization, error) {
	var lastErr error
	for range maxSubdomainAttempts {
		subdomain, err := randomAlphaNum(6)
		if err != nil {
			return sqlcgen.Organization{}, err
		}
		org, err := p.svc.CreatePending(ctx, subdomain)
		if err == nil {
			return org, nil
		}
		lastErr = err
	}
	return sqlcgen.Organization{}, lastErr
}

func (p *Provisioner) provisionOne(ctx context.Context, orgID string) (sqlcgen.Organization, error) {
	ctx, cancel := context.WithTimeout(ctx, provisionTimeout)
	defer cancel()
	if err := p.svc.Provision(ctx, orgID); err != nil {
		return sqlcgen.Organization{}, err
	}
	return p.svc.queries.GetOrganization(ctx, orgID)
}

func (p *Provisioner) push(ctx context.Context, org sqlcgen.Organization) error {
	select {
	case p.ready <- org:
		return nil
	case <-ctx.Done():
		return errors.New("provisioner stopped while waiting to pool organization")
	}
}
