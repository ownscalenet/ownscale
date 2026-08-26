package orgs

import (
	"context"
	"log/slog"
	"time"
)

// defaultPollInterval bounds how long a pending organization can sit
// unprovisioned if its Signal is ever missed (e.g. the process was
// restarted between CreatePending and the signal being sent). It's a
// safety net, not the common path -- see Signal.
const defaultPollInterval = 60 * time.Second

// provisionTimeout bounds how long a single organization's provisioning
// (Docker container creation, image pulls, waiting for headplane to start
// serving, ...) is allowed to run. Run processes organizations one at a
// time from a single goroutine, so without a bound here, one truly stuck
// attempt would block every other organization from ever being
// provisioned.
const provisionTimeout = 2 * time.Minute

// Provisioner runs organization provisioning (Service.Provision) in the
// background, off the request path that creates the pending organizations
// row. Creating an organization's Docker network is picked up by the host
// OS as a network change, which causes browsers (notably Firefox) to drop
// in-flight requests; running it here, instead of inline in the HTTP
// handler that inserts the pending row, avoids that entirely.
//
// It provisions organizations two ways:
//   - immediately, when Signal(orgID) is called (the common case: right
//     after a web request creates a pending organization)
//   - on every poll tick, by picking up any organization still "pending"
//     (covers a missed/lost signal, e.g. a restart between the DB insert
//     and the signal being sent)
//
// Run processes both from a single goroutine, so two attempts to
// provision the same organization can never race each other.
type Provisioner struct {
	svc          *Service
	logger       *slog.Logger
	pollInterval time.Duration
	notify       chan string
}

// NewProvisioner builds a Provisioner backed by svc. If pollInterval is
// zero, defaultPollInterval is used.
func NewProvisioner(svc *Service, logger *slog.Logger, pollInterval time.Duration) *Provisioner {
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	return &Provisioner{
		svc:          svc,
		logger:       logger,
		pollInterval: pollInterval,
		// Buffered so Signal never blocks the caller (the HTTP handler)
		// waiting for Run's loop to catch up; a handful of concurrent
		// organization creations is far more than expected in practice.
		notify: make(chan string, 16),
	}
}

// Signal requests that orgID be provisioned as soon as possible, without
// waiting for the next poll tick. It never blocks: if the notify channel
// is full (Run is falling behind), the signal is dropped and orgID is
// still picked up by the next poll tick.
func (p *Provisioner) Signal(orgID string) {
	select {
	case p.notify <- orgID:
	default:
		p.logger.Warn("provisioner notify channel full, dropping signal (will be picked up by next poll)", "organization_id", orgID)
	}
}

// Run provisions pending organizations until ctx is canceled, at which
// point it returns nil. It provisions once on start (so organizations left
// pending across a restart don't wait a full pollInterval), then again on
// every pollInterval tick or Signal, in a single goroutine so provisioning
// attempts for a given organization are never run concurrently with each
// other.
func (p *Provisioner) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	p.provisionPending(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case orgID := <-p.notify:
			p.provisionOne(ctx, orgID)
		case <-ticker.C:
			p.provisionPending(ctx)
		}
	}
}

// provisionPending provisions every organization currently "pending".
func (p *Provisioner) provisionPending(ctx context.Context) {
	pending, err := p.svc.queries.ListPendingOrganizations(ctx)
	if err != nil {
		p.logger.Error("list pending organizations", "error", err)
		return
	}
	for _, org := range pending {
		p.provisionOne(ctx, org.ID)
	}
}

// provisionOne provisions a single organization, logging (rather than
// returning) any error since it's run from Run's loop with no caller left
// to hand the error to. It bounds the attempt with provisionTimeout so a
// stuck provision can't block every other organization forever.
func (p *Provisioner) provisionOne(ctx context.Context, orgID string) {
	ctx, cancel := context.WithTimeout(ctx, provisionTimeout)
	defer cancel()
	if err := p.svc.Provision(ctx, orgID); err != nil {
		p.logger.Error("provision organization", "organization_id", orgID, "error", err)
	}
}
