// Package orgs provisions and manages organizations and their in-process
// headscale instances.
package orgs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/ownscalenet/headscale/hscontrol"
	"github.com/rs/xid"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/errgroup"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/headscaleclient"
	"github.com/ownscalenet/ownscale/utils"
)

const (
	maxSubdomainAttempts = 5
	initialPort          = 10_000
)

// fixedSubdomains are handed out in order to the first organizations
// created, so development identities land on predictable subdomains.
var fixedSubdomains = []string{"aaaa", "bbbb", "cccc", "dddd", "eeee", "ffff"}

// Service provisions organizations and manages the lifecycle of every
// org's in-process hscontrol.Headscale instance.
type Service struct {
	queries    *db.TxQueries
	baseDomain string
	issuer     string
	derpCfg    config.DerpConfig
	logger     *slog.Logger
	dataDir    string

	instances utils.Map[string, *hscontrol.Headscale]
	ports     utils.Map[string, int]
	grp       *errgroup.Group

	// startMu serializes headscale startup (headscale's DERP map shuffling
	// shares one non-thread-safe rand.Rand across instances) and guards
	// nextPort, which is only ever handed out as part of a startup.
	// This is not enough no prevent races, there's still a race because the
	// derp worker (usually at 3h interval) calls GetDERPMap, which again
	// touches the global rand.Rand.
	startMu  sync.Mutex
	nextPort int

	// createFn defaults to createHeadscale; override in tests to skip
	// real headscale startup.
	createFn func(ctx context.Context, dataDir, subdomain, baseDomain string, port int, oidcCreds OIDCClientCredentials, derpCfg config.DerpConfig) (*hscontrol.Headscale, error)
}

// NewService builds a Service that manages organizations under baseDomain.
func NewService(queries *db.TxQueries, baseDomain, issuer string, derpCfg config.DerpConfig, logger *slog.Logger, dataDir string) *Service {
	grp := &errgroup.Group{}
	// Keep the errgroup alive even when no org goroutines are running yet.
	grp.Go(func() error {
		time.Sleep(999999 * time.Hour)
		return nil
	})
	return &Service{
		queries:    queries,
		baseDomain: baseDomain,
		issuer:     issuer,
		derpCfg:    derpCfg,
		logger:     logger,
		dataDir:    dataDir,
		nextPort:   initialPort,
		grp:        grp,
		createFn:   createHeadscale,
	}
}

// BaseDomain returns the domain organizations are provisioned under.
func (s *Service) BaseDomain() string {
	return s.baseDomain
}

// ForOrg instantiates an OrgService for the given organization.
func (s *Service) ForOrg(org sqlcgen.Organization) *OrgService {
	sub := org.Subdomain
	return &OrgService{
		queries:   s.queries,
		orgID:     org.ID,
		subdomain: sub,
		getHeadscale: func() (*hscontrol.Headscale, bool) {
			return s.instances.Load(sub)
		},
	}
}

// MintClient implements headscaleclient.ClientMinter: it creates a fresh
// API key via the headscale instance's in-process state and returns a
// Client pointed at http://127.0.0.1:{port}.
func (s *Service) MintClient(ctx context.Context, subdomain string) (_ *headscaleclient.Client, err error) {
	defer derrors.Wrap(&err, "s.MintClient(%q)", subdomain)
	app, ok := s.instances.Load(subdomain)
	if !ok {
		return nil, fmt.Errorf("no headscale instance running for %q", subdomain)
	}
	port, _ := s.ports.Load(subdomain)

	expiry := time.Now().Add(87600 * time.Hour) // 10 years
	key, _, err := app.GetState().CreateAPIKey(&expiry)
	if err != nil {
		return nil, fmt.Errorf("create headscale API key: %w", err)
	}
	return headscaleclient.New(fmt.Sprintf("127.0.0.1:%d", port), key), nil
}

// LookupPort returns the TCP port assigned to subdomain's headscale
// instance, or false if none is running.
func (s *Service) LookupPort(subdomain string) (int, bool) {
	return s.ports.Load(subdomain)
}

// Start restarts the headscale instances of existing organizations. It
// returns once startup recovery is complete.
func (s *Service) Start(ctx context.Context) error {
	if err := s.recoverActive(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// NextSubdomain returns an unused subdomain for a new organization: the
// first free one of fixedSubdomains, else a random one.
func (s *Service) NextSubdomain(ctx context.Context) (_ string, err error) {
	defer derrors.Wrap(&err, "s.NextSubdomain()")

	candidates := slices.Clone(fixedSubdomains)
	for range maxSubdomainAttempts {
		suffix, err := randomAlphaNum(6)
		if err != nil {
			return "", err
		}
		candidates = append(candidates, suffix)
	}
	for _, subdomain := range candidates {
		_, err := s.queries.GetOrganizationBySubdomain(ctx, subdomain)
		if errors.Is(err, sql.ErrNoRows) {
			return subdomain, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("no unused subdomain found")
}

// Create provisions a new active organization with the given subdomain and
// starts its headscale instance.
func (s *Service) Create(ctx context.Context, subdomain string) (_ sqlcgen.Organization, err error) {
	defer derrors.Wrap(&err, "s.Create(%q)", subdomain)

	if err := ValidateSubdomain(subdomain); err != nil {
		return sqlcgen.Organization{}, err
	}

	clientID, err := randomAlphaNum(8)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	clientID = "os_" + subdomain + "_" + clientID
	clientSecret, err := randomAlphaNum(32)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	clientSecretHash, err := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	redirectURIsJSON, err := json.Marshal([]string{
		"https://" + subdomain + "." + s.baseDomain + "/oidc/callback",
		"https://" + subdomain + "." + s.baseDomain + "/admin/oidc/callback",
	})
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	oidcCreds := OIDCClientCredentials{
		Issuer:       s.issuer,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
	hsConfig, err := BuildHeadscaleConfig(subdomain, subdomain+"."+s.baseDomain, oidcCreds, s.derpCfg)
	if err != nil {
		return sqlcgen.Organization{}, err
	}

	port, err := s.startHeadscale(ctx, subdomain, 0, oidcCreds, s.derpCfg)
	if err != nil {
		return sqlcgen.Organization{}, err
	}

	return s.insertProvisioned(ctx, subdomain, StatusActive, port, clientID, string(clientSecretHash), string(redirectURIsJSON), hsConfig)
}

func (s *Service) insertProvisioned(ctx context.Context, subdomain, status string, port int, clientID, clientSecretHash, redirectURIsJSON, hsConfig string) (_ sqlcgen.Organization, err error) {
	txQueries, commit, rollback, err := s.queries.StartTx(ctx)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	defer rollback()

	org, err := txQueries.CreateOrganization(ctx, sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: subdomain,
		Status:    status,
		Port:      int64(port),
	})
	if err != nil {
		return sqlcgen.Organization{}, fmt.Errorf("create organization record (already exists?): %w", err)
	}
	if _, err := txQueries.CreateOIDCClient(ctx, sqlcgen.CreateOIDCClientParams{
		ID:               xid.New().String(),
		OrganizationID:   org.ID,
		ClientID:         clientID,
		ClientSecretHash: clientSecretHash,
		RedirectUris:     redirectURIsJSON,
	}); err != nil {
		return sqlcgen.Organization{}, err
	}
	if _, err := txQueries.CreateOrgConfig(ctx, sqlcgen.CreateOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: org.ID,
		Path:           ConfigYAMLPath,
		Content:        hsConfig,
	}); err != nil {
		return sqlcgen.Organization{}, err
	}
	if err := commit(); err != nil {
		return sqlcgen.Organization{}, err
	}
	return org, nil
}

// recoverActive starts headscale instances for all active
// organizations that were running before this process restart.
func (s *Service) recoverActive(ctx context.Context) error {
	orgs, err := s.queries.ListOrganizations(ctx)
	if err != nil {
		return err
	}
	for _, org := range orgs {
		if org.Status != StatusActive && org.Status != StatusUpdatePending {
			continue
		}
		cfgRow, err := s.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
			OrganizationID: org.ID,
			Path:           ConfigYAMLPath,
		})
		if err != nil {
			s.logger.Warn("load org config for recovery; skipping", "subdomain", org.Subdomain, "error", err)
			continue
		}
		parsedCfg, err := ParseHeadscaleFileConfig(cfgRow.Content)
		if err != nil {
			s.logger.Warn("parse org config for recovery; skipping", "subdomain", org.Subdomain, "error", err)
			continue
		}
		oidcCreds := OIDCClientCredentials{
			Issuer:       parsedCfg.OIDC.Issuer,
			ClientID:     parsedCfg.OIDC.ClientID,
			ClientSecret: parsedCfg.OIDC.ClientSecret,
		}

		if _, err := s.startHeadscale(ctx, org.Subdomain, int(org.Port), oidcCreds, s.derpCfg); err != nil {
			s.logger.Error("recover active org headscale instance", "subdomain", org.Subdomain, "error", err)
		}
	}
	return nil
}

// startHeadscale creates and launches an hscontrol.Headscale instance for
// subdomain, registering it in the in-memory maps, and returns the port it
// listens on. A port of 0 allocates the next free one; otherwise port is an
// already-persisted port that must not be handed out again. If headscale's
// DB has no policy yet the default ACL policy is written before Serve
// starts, so GetPolicy never returns "acl policy not found".
//
// Instances start one at a time: this returns only once Serve has loaded
// its DERP map, or has failed.
func (s *Service) startHeadscale(ctx context.Context, subdomain string, port int, oidcCreds OIDCClientCredentials, derpCfg config.DerpConfig) (_ int, err error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if port == 0 {
		port = s.nextPort
	}
	s.nextPort = max(s.nextPort, port+1)

	app, err := s.createFn(ctx, s.dataDir, subdomain, s.baseDomain, port, oidcCreds, derpCfg)
	if err != nil {
		return 0, fmt.Errorf("create headscale instance for %q: %w", subdomain, err)
	}
	s.ports.Store(subdomain, port)
	if app == nil {
		return port, nil
	}

	if _, err := app.GetState().GetPolicy(); err != nil {
		if _, err := app.GetState().SetPolicyInDB(defaultACLPolicy); err != nil {
			s.logger.Warn("seed default ACL policy", "subdomain", subdomain, "error", err)
		}
	}
	s.instances.Store(subdomain, app)

	exited := make(chan struct{})
	// TODO: tie to a per-org context so orgs can be stopped and restarted.
	s.grp.Go(func() error {
		defer close(exited)
		return app.Serve(context.Background())
	})
	waitForDERPMap(ctx, app, exited)
	return port, nil
}

// waitForDERPMap blocks until app's Serve has loaded the DERP map, Serve
// has exited, or ctx is done.
func waitForDERPMap(ctx context.Context, app *hscontrol.Headscale, exited <-chan struct{}) {
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for !app.GetState().DERPMap().Valid() {
		select {
		case <-t.C:
		case <-exited:
			return
		case <-ctx.Done():
			return
		}
	}
}

// randomAlphaNum generates a random alphanumeric string of n characters
// prefixed with "os".
func randomAlphaNum(n int) (_ string, err error) {
	defer derrors.Wrap(&err, "orgs.randomAlphaNum()")

	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	suffix := make([]byte, n)
	for i, b := range buf {
		suffix[i] = chars[int(b)%len(chars)]
	}
	return "os" + string(suffix), nil
}

// Organization lifecycle statuses. These must stay in sync with the CHECK
// constraint added to that column in migrations.
const (
	// StatusInactive does not yet have a use.
	StatusInactive = "inactive"
	// StatusActive is set on a provisioned organization.
	StatusActive = "active"
	// StatusUpdatePending is set while an organization's headscale config is applied.
	StatusUpdatePending = "update_pending"
)
