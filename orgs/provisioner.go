package orgs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
var DefaultSeedSubdomains = []string{"aaaa", "bbbb", "cccc", "dddd", "eeee", "ffff"}

// Provisioner keeps a small pool of fully-provisioned, not-yet-assigned
// organizations ready to hand out.
type Provisioner struct {
	svc              *Service
	logger           *slog.Logger
	retryBackoff     time.Duration
	failureThreshold int
	longBackoff      time.Duration
	ready            chan sqlcgen.Organization
	maxOrgs          int
}

func NewProvisioner(svc *Service, logger *slog.Logger, retryBackoff time.Duration, queueCapacity, maxOrgs int) *Provisioner {
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
		maxOrgs:          maxOrgs,
	}
}

// Take removes a fully-provisioned organization from the pool, blocking
// until one is available or ctx is done (in which case it returns
// ctx.Err())
func (p *Provisioner) Take(ctx context.Context) (sqlcgen.Organization, error) {
	select {
	case org := <-p.ready:
		claimed, err := p.svc.queries.UpdateOrganizationStatus(ctx, sqlcgen.UpdateOrganizationStatusParams{
			ID:     org.ID,
			Status: StatusActive,
		})
		if err != nil {
			return sqlcgen.Organization{}, err
		}
		return claimed, nil
	case <-ctx.Done():
		return sqlcgen.Organization{}, ctx.Err()
	}
}

// Run recovers any organizations left "ready" from a previous restart,
// then replenishes the pool forever.
func (p *Provisioner) Run(ctx context.Context) error {
	if err := p.seedFixed(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if err := p.recoverReady(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	var consecutiveFailures int
	for {
		// See OWNSCALE_PROVISIONER_MAX_ORGS. Counted from the database
		// (not an in-memory field) so the cap survives process restarts.
		if p.maxOrgs > 0 {
			active, err := p.svc.queries.CountActiveOrganizations(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if active >= int64(p.maxOrgs) {
				p.logger.Info("provisioner reached max_orgs; no longer replenishing", "max_orgs", p.maxOrgs)
				<-ctx.Done()
				return nil
			}
		}
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
		} else {
			if consecutiveFailures > 0 {
				p.logger.Info("provisioner recovered after consecutive failures; resuming normal cadence",
					"consecutive_failures", consecutiveFailures)
				consecutiveFailures = 0
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// seedFixed makes sure every subdomain in DefaultSeedSubdomains has an
// organization record, fully provisioning and inserting it (status
// "ready") if missing. Safe to call on every restart: existing
// organizations (in any status) are left untouched. Newly-seeded orgs are
// picked up and pooled by recoverReady, run right after this.
func (p *Provisioner) seedFixed(ctx context.Context) error {
	for _, subdomain := range DefaultSeedSubdomains {
		_, err := p.svc.queries.GetOrganizationBySubdomain(ctx, subdomain)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := p.provisionOne(ctx, subdomain); err != nil {
			return err
		}
	}
	return nil
}

// recoverReady re-pools every organization left "ready" in the database.
func (p *Provisioner) recoverReady(ctx context.Context) error {
	ready, err := p.svc.queries.ListReadyOrganizations(ctx)
	if err != nil {
		return err
	}
	for _, org := range ready {
		if err := p.push(ctx, org); err != nil {
			return err
		}
	}
	return nil
}

// replenishOne provisions a new organization under a randomly generated
// subdomain (retrying on collision up to maxSubdomainAttempts times) and
// pushes it onto the pool, blocking if the pool is already full.
func (p *Provisioner) replenishOne(ctx context.Context) error {
	var lastErr error
	for range maxSubdomainAttempts {
		subdomain, err := randomAlphaNum(6)
		if err != nil {
			return err
		}
		_, err = p.svc.queries.GetOrganizationBySubdomain(ctx, subdomain)
		if err == nil {
			lastErr = fmt.Errorf("subdomain %q already in use", subdomain)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		org, err := p.provisionOne(ctx, subdomain)
		if err != nil {
			return err
		}
		return p.push(ctx, org)
	}
	return lastErr
}

// provisionOne provisions and inserts subdomain as a "ready" organization
// and returns it.
func (p *Provisioner) provisionOne(ctx context.Context, subdomain string) (sqlcgen.Organization, error) {
	ctx, cancel := context.WithTimeout(ctx, provisionTimeout)
	defer cancel()
	return p.svc.provisionNew(ctx, subdomain, StatusReady)
}

func (p *Provisioner) push(ctx context.Context, org sqlcgen.Organization) error {
	select {
	case p.ready <- org:
		return nil
	case <-ctx.Done():
		return errors.New("provisioner stopped while waiting to pool organization")
	}
}
