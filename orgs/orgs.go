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
	"net"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ownscalenet/headscale/hscontrol"
	"github.com/ownscalenet/headscale/hscontrol/types"
	"github.com/ownscalenet/headscale/hscontrol/util"
	"github.com/rs/xid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/headscaleclient"
	"github.com/ownscalenet/ownscale/utils"
)

const (
	maxSubdomainAttempts = 5

	// Instance ports are allocated from firstPort up, below the ephemeral
	// port ranges (Linux: 32768+, IANA: 49152+): an ephemeral port that is
	// free when allocated can be taken by an outgoing connection's source
	// port before headscale listens on it.
	firstPort = 10_000
	lastPort  = 32_767
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

	orgs utils.Map[string, *orgRuntime]

	// startMu serializes headscale startup (headscale's DERP map shuffling
	// shares one non-thread-safe rand.Rand across instances) and guards
	// nextPort. This is not enough to prevent races, there's still a race
	// because the derp worker (usually at 3h interval) calls GetDERPMap,
	// which again touches the global rand.Rand.
	startMu  sync.Mutex
	nextPort int

	// createFn defaults to createHeadscale; override in tests to skip
	// real headscale startup.
	createFn func(dataDir, subdomain, baseDomain string, port int, hsCfg *HeadscaleConfig, derpCfg config.DerpConfig) (*hscontrol.Headscale, error)
}

// orgRuntime is an organization's in-memory headscale state. It exists
// once the organization's headscale was first created in this process.
type orgRuntime struct {
	// port is headscale's loopback port, reused across restarts.
	port int
	// inst is nil while headscale is restarting.
	inst atomic.Pointer[instance]

	// conns tracks the connections DialOrg handed out, so a restart can
	// close them: headscale hijacks its device connections, so stopping the
	// server leaves them attached to the stopped instance instead of
	// letting the devices reconnect to the new one.
	conns utils.Map[net.Conn, struct{}]
}

// track registers conn for closing on restart, until it's closed.
func (rt *orgRuntime) track(conn net.Conn) net.Conn {
	tc := &trackedConn{Conn: conn}
	tc.untrack = func() { rt.conns.Delete(tc) }
	rt.conns.Store(tc, struct{}{})
	return tc
}

// closeConns closes every tracked connection.
func (rt *orgRuntime) closeConns() {
	for c := range rt.conns.Range {
		c.Close()
	}
}

// instance is a running headscale server and the means to stop it.
type instance struct {
	app    *hscontrol.Headscale
	client *headscaleclient.Client
	cancel context.CancelFunc
	// exited is closed once app.Serve has returned.
	exited chan struct{}
}

// NewService builds a Service that manages organizations under baseDomain.
func NewService(queries *db.TxQueries, baseDomain, issuer string, derpCfg config.DerpConfig, logger *slog.Logger, dataDir string) *Service {
	return &Service{
		queries:    queries,
		baseDomain: baseDomain,
		issuer:     issuer,
		derpCfg:    derpCfg,
		logger:     logger,
		dataDir:    dataDir,
		nextPort:   firstPort,
		createFn:   createHeadscale,
	}
}

// ForOrg instantiates an OrgService for the given organization.
func (s *Service) ForOrg(org sqlcgen.Organization) *OrgService {
	sub := org.Subdomain
	return &OrgService{
		queries:   s.queries,
		orgID:     org.ID,
		subdomain: sub,
		restart: func(ctx context.Context) error {
			return s.restartHeadscale(ctx, sub)
		},
	}
}

// running returns subdomain's running headscale instance.
func (s *Service) running(subdomain string) (*instance, bool) {
	rt, ok := s.orgs.Load(subdomain)
	if !ok {
		return nil, false
	}
	inst := rt.inst.Load()
	return inst, inst != nil
}

// Client returns a client for subdomain's running headscale instance.
func (s *Service) Client(subdomain string) (*headscaleclient.Client, error) {
	inst, ok := s.running(subdomain)
	if !ok {
		return nil, ErrNotRunning
	}
	return inst.client, nil
}

// ErrNotRunning is returned for an organization without a running
// headscale instance.
var ErrNotRunning = errors.New("organization not running")

// DialOrg connects to subdomain's headscale instance. The connection is
// closed when the instance is restarted.
func (s *Service) DialOrg(ctx context.Context, subdomain string) (net.Conn, error) {
	rt, ok := s.orgs.Load(subdomain)
	if !ok {
		return nil, ErrNotRunning
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", rt.port))
	if err != nil {
		return nil, err
	}
	return rt.track(conn), nil
}

// trackedConn removes itself from its orgRuntime's tracking when closed.
type trackedConn struct {
	net.Conn
	untrack func()
	once    sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(c.untrack)
	return c.Conn.Close()
}

// SetOIDCIdentity gives subdomain's headscale user headscaleUserID the OIDC
// identity headscale derives from an ownscale login of the user subject.
// Without it, that person's first OIDC login through headscale creates a
// second headscale user, as headscale only matches existing users by this
// identity. Users that already have an identity, and identities already
// owned by another user, are left alone.
func (s *Service) SetOIDCIdentity(ctx context.Context, subdomain, headscaleUserID, subject string) (err error) {
	defer derrors.Wrap(&err, "s.SetOIDCIdentity(%q, %q)", subdomain, headscaleUserID)

	inst, ok := s.running(subdomain)
	if !ok {
		return ErrNotRunning
	}
	return s.setOIDCIdentity(inst.app, headscaleUserID, subject)
}

func (s *Service) setOIDCIdentity(app *hscontrol.Headscale, headscaleUserID, subject string) error {
	uid, err := strconv.ParseUint(headscaleUserID, 10, 64)
	if err != nil {
		return err
	}
	state := app.GetState()
	identifier := (&types.OIDCClaims{Iss: s.issuer, Sub: subject}).Identifier()
	if owner, err := state.GetUserByOIDCIdentifier(identifier); err == nil && owner != nil {
		return nil // already set, or owned by another user
	}

	_, c, err := state.UpdateUser(types.UserID(uid), func(u *types.User) error {
		if u.ProviderIdentifier.Valid {
			return nil
		}
		u.ProviderIdentifier = sql.NullString{String: identifier, Valid: true}
		u.Provider = util.RegisterMethodOIDC
		return nil
	})
	if err != nil {
		return err
	}
	app.Change(c)
	return nil
}

// LookupPort returns the TCP port assigned to subdomain's headscale
// instance, or false if none is running.
func (s *Service) LookupPort(subdomain string) (int, bool) {
	rt, ok := s.orgs.Load(subdomain)
	if !ok {
		return 0, false
	}
	return rt.port, true
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
	hsCfg := newHeadscaleConfig(subdomain, OIDCConfig{Issuer: s.issuer, ClientID: clientID, ClientSecret: clientSecret})
	hsConfig, err := hsCfg.Marshal()
	if err != nil {
		return sqlcgen.Organization{}, err
	}

	// Insert first: the unique subdomain constraint must reject a duplicate
	// before its headscale would replace the running one.
	org, err := s.insertProvisioned(ctx, subdomain, clientID, string(clientSecretHash), string(redirectURIsJSON), hsConfig)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	if err := s.startHeadscale(ctx, subdomain, hsCfg); err != nil {
		if delErr := s.queries.DeleteOrganization(context.WithoutCancel(ctx), org.ID); delErr != nil {
			err = errors.Join(err, fmt.Errorf("delete organization: %w", delErr))
		}
		return sqlcgen.Organization{}, err
	}
	return org, nil
}

func (s *Service) insertProvisioned(ctx context.Context, subdomain, clientID, clientSecretHash, redirectURIsJSON, hsConfig string) (_ sqlcgen.Organization, err error) {
	txQueries, commit, rollback, err := s.queries.StartTx(ctx)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	defer rollback()

	org, err := txQueries.CreateOrganization(ctx, sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: subdomain,
		Status:    StatusActive,
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
		Path:           HeadscaleConfigKey,
		Content:        hsConfig,
	}); err != nil {
		return sqlcgen.Organization{}, err
	}
	if err := commit(); err != nil {
		return sqlcgen.Organization{}, err
	}
	return org, nil
}

// Start starts the headscale instances of existing organizations. It
// returns once all of them have started or failed to.
func (s *Service) Start(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "s.Start()")

	orgs, err := s.queries.ListOrganizations(ctx)
	if err != nil {
		return err
	}
	for _, org := range orgs {
		cfgRow, err := s.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
			OrganizationID: org.ID,
			Path:           HeadscaleConfigKey,
		})
		if err != nil {
			s.logger.Warn("load org config for recovery; skipping", "subdomain", org.Subdomain, "error", err)
			continue
		}
		parsedCfg, err := parseHeadscaleConfig(cfgRow.Content)
		if err != nil {
			s.logger.Warn("parse org config for recovery; skipping", "subdomain", org.Subdomain, "error", err)
			continue
		}

		if err := s.startHeadscale(ctx, org.Subdomain, parsedCfg); err != nil {
			s.logger.Error("recover active org headscale instance", "subdomain", org.Subdomain, "error", err)
		}
	}
	return nil
}

// startHeadscale creates and launches an hscontrol.Headscale instance for
// subdomain, registering it in s.orgs. It listens on a free
// loopback port, reused across restarts of the instance within this
// process. If headscale's DB has no policy yet the default ACL policy is
// written before Serve starts, so GetPolicy never returns "acl policy not
// found".
//
// Instances start one at a time: this returns only once Serve has loaded
// its DERP map, or has failed.
func (s *Service) startHeadscale(ctx context.Context, subdomain string, hsCfg *HeadscaleConfig) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	return s.startLocked(ctx, subdomain, hsCfg)
}

// startLocked is startHeadscale for callers already holding startMu.
func (s *Service) startLocked(ctx context.Context, subdomain string, hsCfg *HeadscaleConfig) error {
	rt, ok := s.orgs.Load(subdomain)
	port := 0
	if ok {
		port = rt.port
	} else {
		var err error
		if port, err = s.allocatePort(); err != nil {
			return err
		}
	}

	app, err := s.createFn(s.dataDir, subdomain, s.baseDomain, port, hsCfg, s.derpCfg)
	if err != nil {
		return fmt.Errorf("create headscale instance for %q: %w", subdomain, err)
	}
	if !ok {
		rt = &orgRuntime{port: port}
		s.orgs.Store(subdomain, rt)
	}
	if app == nil {
		return nil
	}

	if _, err := app.GetState().GetPolicy(); err != nil {
		if _, err := app.GetState().SetPolicyInDB(defaultACLPolicy); err != nil {
			s.logger.Warn("seed default ACL policy", "subdomain", subdomain, "error", err)
		}
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	inst := &instance{
		app:    app,
		client: headscaleclient.NewUnix(SocketPath(s.dataDir, subdomain)),
		cancel: cancel,
		exited: make(chan struct{}),
	}
	rt.inst.Store(inst)
	go func() {
		defer close(inst.exited)
		if err := app.Serve(serveCtx); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("headscale stopped", "subdomain", subdomain, "error", err)
		}
	}()
	waitForDERPMap(ctx, app, inst.exited)
	return nil
}

// allocatePort returns the next port from s.nextPort that is free on
// 127.0.0.1. Callers hold startMu.
func (s *Service) allocatePort() (int, error) {
	for port := s.nextPort; port <= lastPort; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		ln.Close()
		s.nextPort = port + 1
		return port, nil
	}
	return 0, errors.New("no free port left for headscale")
}

// restartHeadscale stops subdomain's running headscale instance (if any) and
// starts a new one from the organization's latest stored config, on the same
// port. It starts an instance that isn't running yet.
func (s *Service) restartHeadscale(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "s.restartHeadscale(%q)", subdomain)

	s.startMu.Lock()
	defer s.startMu.Unlock()

	org, err := s.queries.GetOrganizationBySubdomain(ctx, subdomain)
	if err != nil {
		return err
	}
	cfgRow, err := s.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: org.ID,
		Path:           HeadscaleConfigKey,
	})
	if err != nil {
		return err
	}
	hsCfg, err := parseHeadscaleConfig(cfgRow.Content)
	if err != nil {
		return err
	}

	if rt, ok := s.orgs.Load(subdomain); ok {
		// Cleared only once stopped, so a timed-out stop is retried by the
		// next restart instead of leaving it running unreferenced.
		if inst := rt.inst.Load(); inst != nil {
			inst.cancel()
			select {
			case <-inst.exited:
			case <-ctx.Done():
				return fmt.Errorf("waiting for headscale to stop: %w", ctx.Err())
			}
			rt.inst.Store(nil)
		}
		rt.closeConns()
	}
	return s.startLocked(ctx, subdomain, hsCfg)
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
	// StatusActive is set on a provisioned organization.
	StatusActive = "active"
	// StatusUpdatePending is set while an organization's headscale config is applied.
	StatusUpdatePending = "update_pending"
)
