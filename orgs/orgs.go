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
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juanfont/headscale/hscontrol"
	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/juanfont/headscale/hscontrol/util"
	"github.com/rs/xid"
	"golang.org/x/crypto/bcrypt"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"

	"github.com/ownscalenet/ownscale/backup"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/utils"
)

const (
	maxSubdomainAttempts = 5

	// maxBindAttempts bounds how often a start retries on a fresh port
	// when another process took the port before headscale listened on it.
	maxBindAttempts = 3

	// idleTimeout is how long an organization's headscale runs unused
	// before RunSleeper stops it.
	idleTimeout = 30 * time.Minute
	// sweepInterval is how often RunSleeper looks for idle organizations.
	sweepInterval = 2 * time.Minute
	// sleepTimeout bounds waiting for a headscale instance to stop.
	sleepTimeout = 30 * time.Second
	// startTimeout bounds starting a headscale instance.
	startTimeout = 30 * time.Second
)

// Service provisions organizations and manages the lifecycle of every
// org's in-process hscontrol.Headscale instance.
type Service struct {
	queries *db.TxQueries
	cfg     *config.Config
	issuer  string
	logger  *slog.Logger
	backups *backup.Replicator

	orgs utils.Map[string, *orgRuntime]
	// derpMap is the last DERP map RunDERPMapUpdater fetched.
	derpMap atomic.Pointer[tailcfg.DERPMap]
	// restarts counts each org's in-flight restarts, including those still
	// waiting on their orgLock; a counter as two settings saves can overlap.
	restarts utils.Map[string, *atomic.Int32]
	// startFailures holds when Ensure last failed to start each org, so
	// device traffic can't retry a broken org on every request.
	startFailures utils.Map[string, time.Time]
	// onSleep, if set, runs once an org's headscale was put to sleep.
	onSleep func(subdomain string)

	// idleTimeout and sweepInterval are overridden in tests.
	idleTimeout   time.Duration
	sweepInterval time.Duration

	// orgLocks holds a lock per subdomain, serializing headscale
	// start/stop/restart/sleep for that organization.
	// The mutexes have to be on this separate map (instead of inside orgRuntime)
	// to be able to get a lock before the orgRuntime was instantiated.
	orgLocks utils.Map[string, *sync.Mutex]
	// closed is set by Close, so no  (and its database) starts
	// while backups shut down.
	closed atomic.Bool

	// certs is nil when node certificates are disabled.
	certs *nodeCerts

	// createFn defaults to createHeadscale; override in tests to skip
	// real headscale startup.
	createFn func(p headscaleParams) (*hscontrol.Headscale, error)
}

func (s *Service) orgLock(subdomain string) *sync.Mutex {
	mu, _ := s.orgLocks.LoadOrStore(subdomain, new(sync.Mutex))
	return mu
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

	// lastActivity is when the org was last used, in Unix nanoseconds.
	lastActivity atomic.Int64
}

// touch records that the org is being used now.
func (rt *orgRuntime) touch() {
	rt.lastActivity.Store(time.Now().UnixNano())
}

// idle reports whether the org has no open connections and wasn't used
// for d before now.
func (rt *orgRuntime) idle(now time.Time, d time.Duration) bool {
	for range rt.conns.Range {
		return false
	}
	return now.Sub(time.Unix(0, rt.lastActivity.Load())) >= d
}

// track registers conn for closing on restart, until it's closed.
func (rt *orgRuntime) track(conn net.Conn) net.Conn {
	rt.touch()
	tc := &trackedConn{Conn: conn}
	tc.untrack = func() {
		rt.conns.Delete(tc)
		rt.touch()
	}
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
	client *hscl.Client
	cancel context.CancelFunc
	// exited is closed once app.Serve has returned err.
	exited chan struct{}
	err    error
}

// NewService builds a Service that manages organizations under
// cfg.BaseDomain. Each organization's headscale database is added to
// backups once it exists; backups may be nil.
func NewService(queries *db.TxQueries, cfg *config.Config, issuer string,
	logger *slog.Logger, backups *backup.Replicator) (_ *Service, err error) {
	defer derrors.Wrap(&err, "orgs.NewService()")

	provider, err := cfg.DNS.ChallengeProvider()
	if err != nil {
		return nil, err
	}
	var certs *nodeCerts
	if provider != nil {
		certs = &nodeCerts{
			provider:           provider,
			providerName:       cfg.DNS.Provider,
			zone:               cfg.DNS.Zone,
			propagationTimeout: cfg.DNS.PropagationTimeout,
			queries:            queries,
			perOrgPerWeek:      cfg.DNS.CertsPerOrgPerWeek,
		}
	}
	return &Service{
		queries:       queries,
		cfg:           cfg,
		issuer:        issuer,
		logger:        logger,
		backups:       backups,
		certs:         certs,
		createFn:      createHeadscale,
		idleTimeout:   idleTimeout,
		sweepInterval: sweepInterval,
	}, nil
}

// CertsEnabled reports whether nodes can get HTTPS certificates, for
// organizations with MagicDNS on.
func (s *Service) CertsEnabled() bool { return s.certs != nil }

// CertsPerOrgPerWeek is how many new certificate names an organization
// may get per week; 0 means no limit.
func (s *Service) CertsPerOrgPerWeek() int { return s.cfg.DNS.CertsPerOrgPerWeek }

// tailnetDomain returns the MagicDNS domain of the organization with label.
func (s *Service) tailnetDomain(label string) string {
	return label + "." + s.cfg.TailnetDomain()
}

// OnSleep sets f to run each time an organization's headscale is put to
// sleep. It must be set before the Service is used.
func (s *Service) OnSleep(f func(subdomain string)) {
	s.onSleep = f
}

// ForOrg instantiates an OrgService for the given organization.
func (s *Service) ForOrg(org sqlcgen.Organization) *OrgService {
	sub := org.Subdomain
	return &OrgService{
		queries:   s.queries,
		orgID:     org.ID,
		subdomain: sub,
		domain:    s.tailnetDomain(org.TailnetLabel),
		restart: func(ctx context.Context) error {
			return s.restartHeadscale(ctx, sub)
		},
		sleep: func(ctx context.Context) error {
			return s.sleepHeadscale(ctx, sub)
		},
		del: func(ctx context.Context) error {
			return s.deleteOrg(ctx, org.ID, sub)
		},
	}
}

// running returns subdomain's running headscale instance. An instance
// whose Serve returned isn't running.
func (s *Service) running(subdomain string) (*instance, bool) {
	rt, ok := s.orgs.Load(subdomain)
	if !ok {
		return nil, false
	}
	inst := rt.inst.Load()
	if inst == nil {
		return nil, false
	}
	select {
	case <-inst.exited:
		return nil, false
	default:
		return inst, true
	}
}

// hasDatabase reports whether subdomain's headscale database exists, which
// its headscale created when it first started.
func (s *Service) hasDatabase(subdomain string) bool {
	_, err := os.Stat(sqlitePath(s.cfg.DataDir, subdomain))
	return err == nil
}

// Status reports the state of subdomain's headscale instance: one of
// StatusUpdatePending, StatusActive, StatusSleeping or StatusUnavailable.
func (s *Service) Status(subdomain string) string {
	if n, ok := s.restarts.Load(subdomain); ok && n.Load() > 0 {
		return StatusUpdatePending
	}
	if _, ok := s.running(subdomain); ok {
		return StatusActive
	}
	if !s.hasDatabase(subdomain) {
		return StatusUnavailable
	}
	if _, ok := s.startFailures.Load(subdomain); ok {
		return StatusUnavailable
	}
	// Sleeping clears the instance; one left behind exited on its own.
	if rt, ok := s.orgs.Load(subdomain); ok && rt.inst.Load() != nil {
		return StatusUnavailable
	}
	return StatusSleeping
}

// touch records that subdomain's org is being used now.
func (s *Service) touch(subdomain string) {
	if rt, ok := s.orgs.Load(subdomain); ok {
		rt.touch()
	}
}

// Client returns a client for subdomain's running headscale instance.
func (s *Service) Client(subdomain string) (*hscl.Client, error) {
	inst, ok := s.running(subdomain)
	if !ok {
		return nil, ErrNotRunning
	}
	s.touch(subdomain)
	return inst.client, nil
}

var (
	// ErrNotRunning is returned for an organization without a running
	// headscale instance.
	ErrNotRunning = errors.New("organization not running")
	// ErrDatabaseMissing is returned for an organization whose headscale
	// database is gone, e.g. lost and not restored with `osa restore`.
	// Starting a fresh one would give its headscale user IDs to other
	// people than the members linked to them.
	ErrDatabaseMissing = errors.New("organization's headscale database is missing")

	// ErrIdentityTaken is returned when giving a headscale user an OIDC
	// identity another headscale user already has.
	ErrIdentityTaken = errors.New("OIDC identity belongs to another headscale user")

	// ErrMaxOrgs is returned by Create when config.Config.MaxOrgs
	// organizations already exist.
	ErrMaxOrgs = errors.New("organization limit reached")

	errClosed = fmt.Errorf("%w: shutting down", ErrNotRunning)
)

// startRetryDelay is how long Ensure fails fast after it failed to start
// an organization.
const startRetryDelay = 30 * time.Second

// Ensure returns a client for subdomain's headscale instance, starting it
// first if it isn't running. For startRetryDelay after a failed start it
// returns ErrNotRunning without trying again. An unknown subdomain gets
// ErrNotRunning, and one whose database is missing ErrDatabaseMissing.
func (s *Service) Ensure(ctx context.Context, subdomain string) (_ *hscl.Client, err error) {
	defer derrors.Wrap(&err, "s.Ensure(%q)", subdomain)

	if inst, ok := s.running(subdomain); ok {
		s.touch(subdomain)
		return inst.client, nil
	}
	s.orgLock(subdomain).Lock()
	defer s.orgLock(subdomain).Unlock()
	// Another caller may have started it while this one waited.
	if inst, ok := s.running(subdomain); ok {
		s.touch(subdomain)
		return inst.client, nil
	}
	if failed, ok := s.startFailures.Load(subdomain); ok && time.Since(failed) < startRetryDelay {
		return nil, ErrNotRunning
	}

	// A canceled request must not abort a start other callers wait on.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), startTimeout)
	defer cancel()
	org, hsCfg, err := s.loadConfig(ctx, subdomain)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotRunning
	}
	if err != nil {
		return nil, err
	}
	if !s.hasDatabase(subdomain) {
		return nil, ErrDatabaseMissing
	}
	if err := s.startLocked(ctx, org.ID, subdomain, hsCfg); err != nil {
		s.startFailures.Store(subdomain, time.Now())
		return nil, err
	}
	s.startFailures.Delete(subdomain)
	inst, ok := s.running(subdomain)
	if !ok {
		return nil, ErrNotRunning // createFn built no instance (tests)
	}
	return inst.client, nil
}

// loadConfig returns subdomain's organization and its stored headscale
// config.
func (s *Service) loadConfig(ctx context.Context, subdomain string) (sqlcgen.Organization, *HeadscaleConfig, error) {
	org, err := s.queries.GetOrganizationBySubdomain(ctx, subdomain)
	if err != nil {
		return sqlcgen.Organization{}, nil, err
	}
	cfgRow, err := s.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: org.ID,
		Path:           HeadscaleConfigKey,
	})
	if err != nil {
		return sqlcgen.Organization{}, nil, err
	}
	hsCfg, err := parseHeadscaleConfig(cfgRow.Content)
	if err != nil {
		return sqlcgen.Organization{}, nil, err
	}
	hsCfg.DNS.BaseDomain = s.tailnetDomain(org.TailnetLabel)
	return org, hsCfg, nil
}

// DialOrg connects to subdomain's headscale instance, starting it first
// (see Ensure) if it isn't running. The connection is closed when the
// instance is restarted or put to sleep.
func (s *Service) DialOrg(ctx context.Context, subdomain string) (net.Conn, error) {
	if _, ok := s.running(subdomain); !ok {
		if _, err := s.Ensure(ctx, subdomain); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotRunning, err)
		}
	}
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
// identity headscale derives from an ownscale login of the user subject,
// so that person's OIDC logins through headscale land on that user, as
// headscale only matches existing users by this identity. It returns
// ErrIdentityTaken if another headscale user already has the identity.
func (s *Service) SetOIDCIdentity(ctx context.Context, subdomain, headscaleUserID, subject string) (err error) {
	defer derrors.Wrap(&err, "s.SetOIDCIdentity(%q, %q)", subdomain, headscaleUserID)

	inst, ok := s.running(subdomain)
	if !ok {
		return ErrNotRunning
	}
	return s.setOIDCIdentity(inst.app, headscaleUserID, subject)
}

// Identifier returns the OIDC identity headscale gives a user that logs in
// as the ownscale user subject, as reported in hscl.User.ProviderID.
func (s *Service) Identifier(subject string) string {
	return (&types.OIDCClaims{Iss: s.issuer, Sub: subject}).Identifier()
}

func (s *Service) setOIDCIdentity(app *hscontrol.Headscale, headscaleUserID, subject string) error {
	uid, err := strconv.ParseUint(headscaleUserID, 10, 64)
	if err != nil {
		return err
	}
	state := app.GetState()
	identifier := s.Identifier(subject)
	if owner, err := state.GetUserByOIDCIdentifier(identifier); err == nil && owner != nil {
		if owner.ID == uint(uid) {
			return nil
		}
		return ErrIdentityTaken
	}

	_, c, err := state.UpdateUser(types.UserID(uid), func(u *types.User) error {
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

// NextSubdomain returns an unused subdomain for a new organization. A
// fixed subdomain is only given to the user it's named after, through
// hint (their email's local part), so test identities land on their own
// organization regardless of sign-in order. Everyone else gets a random one.
func (s *Service) NextSubdomain(ctx context.Context, hint string) (_ string, err error) {
	defer derrors.Wrap(&err, "s.NextSubdomain(%q)", hint)

	if slices.Contains(s.cfg.FixedSubdomains, hint) {
		free, err := s.subdomainFree(ctx, hint)
		if err != nil {
			return "", err
		}
		if free {
			return hint, nil
		}
	}
	for range maxSubdomainAttempts {
		subdomain, err := randomAlphaNum(6)
		if err != nil {
			return "", err
		}
		if slices.Contains(s.cfg.FixedSubdomains, subdomain) {
			continue
		}
		free, err := s.subdomainFree(ctx, subdomain)
		if err != nil {
			return "", err
		}
		if free {
			return subdomain, nil
		}
	}
	return "", errors.New("no unused subdomain found")
}

// subdomainFree reports whether no organization uses subdomain, nor left
// its files behind.
func (s *Service) subdomainFree(ctx context.Context, subdomain string) (bool, error) {
	if s.hasFiles(subdomain) {
		return false, nil
	}
	_, err := s.queries.GetOrganizationBySubdomain(ctx, subdomain)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

// Create stores a new organization with the given subdomain and starts its
// headscale, which creates the headscale database. It's the only way an
// organization gets one (see ErrDatabaseMissing). If headscale doesn't
// start, the organization is deleted again.
func (s *Service) Create(ctx context.Context, subdomain string) (_ sqlcgen.Organization, err error) {
	defer derrors.Wrap(&err, "s.Create(%q)", subdomain)

	if err := ValidateSubdomain(subdomain); err != nil {
		return sqlcgen.Organization{}, err
	}
	// A new org must not inherit a deleted org's headscale database.
	if s.hasFiles(subdomain) {
		return sqlcgen.Organization{}, fmt.Errorf("data directory of subdomain %q already exists", subdomain)
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
	cost := bcrypt.DefaultCost
	if s.cfg.FastOrgs {
		cost = bcrypt.MinCost
	}
	clientSecretHash, err := bcrypt.GenerateFromPassword([]byte(clientSecret), cost)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	redirectURIsJSON, err := json.Marshal([]string{
		"https://" + subdomain + "." + s.cfg.BaseDomain + "/oidc/callback",
		"https://" + subdomain + "." + s.cfg.BaseDomain + "/admin/oidc/callback",
	})
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	hsCfg := newHeadscaleConfig(OIDCConfig{Issuer: s.issuer, ClientID: clientID, ClientSecret: clientSecret})
	hsConfig, err := hsCfg.Marshal()
	if err != nil {
		return sqlcgen.Organization{}, err
	}

	// maxLabelAttempts bounds retries when a new tailnet label
	// collides with an existing one.
	const maxLabelAttempts = 5
	var org sqlcgen.Organization
	for attempt := 1; ; attempt++ {
		org, err = s.insertOrganization(ctx, subdomain, newTailnetLabel(), clientID, string(clientSecretHash), string(redirectURIsJSON), hsConfig)
		if err != nil && strings.Contains(err.Error(), "organizations.tailnet_label") && attempt < maxLabelAttempts {
			continue
		}
		if err != nil {
			return sqlcgen.Organization{}, err
		}
		break
	}

	s.orgLock(subdomain).Lock()
	defer s.orgLock(subdomain).Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), startTimeout)
	defer cancel()
	// The stored config, as Ensure would start it with.
	_, startCfg, err := s.loadConfig(ctx, subdomain)
	if err == nil {
		err = s.startLocked(ctx, org.ID, subdomain, startCfg)
	}
	if err != nil {
		if delErr := s.queries.DeleteOrganization(ctx, org.ID); delErr != nil {
			s.logger.Error("delete organization whose headscale didn't start", "subdomain", subdomain, "error", delErr)
		} else if rmErr := s.removeFiles(ctx, subdomain); rmErr != nil {
			s.logger.Error("remove files of organization whose headscale didn't start", "subdomain", subdomain, "error", rmErr)
		}
		return sqlcgen.Organization{}, err
	}
	return org, nil
}

// newTailnetLabel returns a random label for an organization's MagicDNS
// domain. It is unrelated to the subdomain, so certificate transparency logs
// don't reveal which organization a node belongs to.
func newTailnetLabel() string {
	return "net" + strings.ToLower(rand.Text()[:6])
}

func (s *Service) insertOrganization(
	ctx context.Context,
	subdomain, tailnetLabel, clientID string,
	clientSecretHash, redirectURIsJSON, hsConfig string) (_ sqlcgen.Organization, err error) {
	txQueries, commit, rollback, err := s.queries.StartTx(ctx)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	defer rollback()

	// Counting in the same transaction as the insert keeps concurrent
	// sign-ups from going over the limit: the pool has one connection, so
	// transactions don't interleave.
	if s.cfg.MaxOrgs > 0 {
		n, err := txQueries.CountOrganizations(ctx)
		if err != nil {
			return sqlcgen.Organization{}, err
		}
		if n >= int64(s.cfg.MaxOrgs) {
			return sqlcgen.Organization{}, ErrMaxOrgs
		}
	}

	org, err := txQueries.CreateOrganization(ctx, sqlcgen.CreateOrganizationParams{
		ID:           xid.New().String(),
		Subdomain:    subdomain,
		TailnetLabel: tailnetLabel,
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

// startLocked creates and launches an hscontrol.Headscale instance for
// subdomain, registering it in s.orgs. It listens on a loopback port
// picked by the kernel, reused across restarts of the instance within this
// process; if another process took that port, it retries on a new one.
// Callers hold subdomain's orgLock, so an org's own instances start one at
// a time; different orgs start concurrently.
//
// It returns once the instance serves, or with the reason it doesn't.
func (s *Service) startLocked(ctx context.Context, orgID, subdomain string, hsCfg *HeadscaleConfig) error {
	if s.closed.Load() {
		return errClosed
	}
	if err := s.ensureNoiseKey(ctx, orgID, subdomain); err != nil {
		return err
	}
	rt, known := s.orgs.Load(subdomain)
	port := 0
	if known {
		port = rt.port
	}
	for attempt := 1; ; attempt++ {
		if port == 0 {
			var err error
			if port, err = freeLoopbackPort(); err != nil {
				return err
			}
		}
		inst, err := s.launch(ctx, orgID, subdomain, port, hsCfg)
		if _, ok := errors.AsType[*types.ListenerBindError](err); ok && attempt < maxBindAttempts {
			s.logger.Warn("headscale port taken, retrying on another", "subdomain", subdomain, "port", port, "error", err)
			port = 0
			continue
		}
		if err != nil {
			return err
		}
		// A changed port gets a new runtime, as readers use rt.port unlocked.
		if !known || rt.port != port {
			rt = &orgRuntime{port: port}
			s.orgs.Store(subdomain, rt)
		}
		if inst != nil {
			rt.inst.Store(inst)
		}
		rt.touch()
		// headscale created its database by now.
		if err := s.backups.Add(DBFile(subdomain)); err != nil {
			s.logger.Error("back up organization database", "subdomain", subdomain, "error", err)
		}
		return nil
	}
}

// launch creates subdomain's headscale on port and serves it. If
// headscale's DB has no policy yet the default ACL policy is written before
// Serve starts, so GetPolicy never returns "acl policy not found". It
// returns once the instance serves on port; otherwise it stops the instance
// and returns why, e.g. a *types.ListenerBindError when port is taken. A
// nil instance means createFn built none (tests).
func (s *Service) launch(ctx context.Context, orgID, subdomain string, port int, hsCfg *HeadscaleConfig) (*instance, error) {
	derpMap, err := s.currentDERPMap(ctx)
	if err != nil {
		return nil, err
	}
	app, err := s.createFn(headscaleParams{
		orgID:      orgID,
		dataDir:    s.cfg.DataDir,
		subdomain:  subdomain,
		baseDomain: s.cfg.BaseDomain,
		port:       port,
		hsCfg:      hsCfg,
		derpMap:    derpMap,
		certs:      s.certs,
		fast:       s.cfg.FastOrgs,
	})
	if err != nil {
		return nil, fmt.Errorf("create headscale instance for %q: %w", subdomain, err)
	}
	if app == nil {
		return nil, nil
	}

	// TODO: it's not nice to call this internal state method. hscl has the same
	// function to set&get the policy over API.
	if _, err := app.GetState().GetPolicy(); err != nil {
		if _, err := app.GetState().SetPolicyInDB(DefaultACLPolicy); err != nil {
			s.logger.Warn("seed default ACL policy", "subdomain", subdomain, "error", err)
		}
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	inst := &instance{
		app:    app,
		client: hscl.NewUnix(SocketPath(s.cfg.DataDir, subdomain)),
		cancel: cancel,
		exited: make(chan struct{}),
	}
	go func() {
		defer close(inst.exited)
		inst.err = app.Serve(serveCtx)
		if inst.err != nil && !errors.Is(inst.err, context.Canceled) {
			s.logger.Error("headscale stopped", "subdomain", subdomain, "error", inst.err)
		}
	}()
	if err := waitServing(ctx, inst, port); err != nil {
		inst.cancel()
		<-inst.exited
		return nil, err
	}
	return inst, nil
}

// waitServing blocks until inst serves on port. It checks that headscale's
// /key answers with inst's own Noise key, as the port may be held by
// another process. It fails if inst's Serve exits or ctx is done first.
func waitServing(ctx context.Context, inst *instance, port int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/key?v=%d", port, tailcfg.CurrentCapabilityVersion)
	want := inst.app.NoisePublicKey()
	client := &http.Client{
		Timeout:   time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		if servesKey(ctx, client, url, want) {
			return nil
		}
		select {
		case <-t.C:
		case <-inst.exited:
			if inst.err == nil {
				return errors.New("headscale exited")
			}
			return fmt.Errorf("headscale exited: %w", inst.err)
		case <-ctx.Done():
			return fmt.Errorf("waiting for headscale to serve: %w", ctx.Err())
		}
	}
}

// servesKey reports whether url answers with Noise public key want.
func servesKey(ctx context.Context, client *http.Client, url string, want key.MachinePublic) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body tailcfg.OverTLSPublicKeyResponse
	return json.NewDecoder(resp.Body).Decode(&body) == nil && body.PublicKey == want
}

// freeLoopbackPort returns a TCP port the kernel reports free on 127.0.0.1.
func freeLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// restartHeadscale stops subdomain's running headscale instance (if any) and
// starts a new one from the organization's latest stored config, on the same
// port. It also starts an instance that isn't running, but none whose
// database is missing.
func (s *Service) restartHeadscale(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "s.restartHeadscale(%q)", subdomain)

	n, _ := s.restarts.LoadOrStore(subdomain, new(atomic.Int32))
	n.Add(1)
	defer n.Add(-1)

	s.orgLock(subdomain).Lock()
	defer s.orgLock(subdomain).Unlock()

	// The stored config applies once the database is restored.
	if !s.hasDatabase(subdomain) {
		return nil
	}
	org, hsCfg, err := s.loadConfig(ctx, subdomain)
	if err != nil {
		return err
	}

	if rt, ok := s.orgs.Load(subdomain); ok {
		if err := stopLocked(ctx, rt); err != nil {
			return err
		}
	}
	if err := s.startLocked(ctx, org.ID, subdomain, hsCfg); err != nil {
		s.startFailures.Store(subdomain, time.Now())
		return err
	}
	s.startFailures.Delete(subdomain)
	return nil
}

// stopLocked stops rt's headscale instance, if any, and closes its
// connections. Callers hold the org's orgLock.
func stopLocked(ctx context.Context, rt *orgRuntime) error {
	// Cleared only once stopped, so a timed-out stop is retried by the next
	// stop instead of leaving it running unreferenced.
	if inst := rt.inst.Load(); inst != nil {
		// Open connections would hold up headscale's HTTP shutdown.
		rt.closeConns()
		inst.cancel()
		select {
		case <-inst.exited:
		case <-ctx.Done():
			return fmt.Errorf("waiting for headscale to stop: %w", ctx.Err())
		}
		rt.inst.Store(nil)
	}
	rt.closeConns()
	return nil
}

// sleepHeadscale stops subdomain's headscale instance, keeping its port for
// when it's woken. It's a no-op if the instance isn't running.
func (s *Service) sleepHeadscale(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "s.sleepHeadscale(%q)", subdomain)

	s.logger.Info("putting org to sleep", "org", subdomain)
	s.orgLock(subdomain).Lock()
	defer s.orgLock(subdomain).Unlock()

	rt, ok := s.orgs.Load(subdomain)
	if !ok || rt.inst.Load() == nil {
		return nil
	}
	if err := stopLocked(ctx, rt); err != nil {
		return err
	}
	if s.onSleep != nil {
		s.onSleep(subdomain)
	}
	return nil
}

// deleteOrg stops subdomain's headscale and deletes the organization with
// everything it owns: its rows, files and backup. Failing to remove the
// files is only logged, as the organization is gone by then.
func (s *Service) deleteOrg(ctx context.Context, orgID, subdomain string) (err error) {
	defer derrors.Wrap(&err, "s.deleteOrg(%q)", subdomain)

	// A canceled request must not leave the org half deleted.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sleepTimeout)
	defer cancel()
	s.orgLock(subdomain).Lock()
	defer s.orgLock(subdomain).Unlock()

	if rt, ok := s.orgs.Load(subdomain); ok {
		if err := stopLocked(ctx, rt); err != nil {
			return err
		}
		s.orgs.Delete(subdomain)
	}
	s.startFailures.Delete(subdomain)
	if s.onSleep != nil {
		s.onSleep(subdomain)
	}
	if err := s.queries.DeleteOrganization(ctx, orgID); err != nil {
		return err
	}
	s.logger.Info("deleted organization", "org", subdomain)
	if err := s.removeFiles(ctx, subdomain); err != nil {
		s.logger.Error("remove files of deleted organization", "org", subdomain, "error", err)
	}
	return nil
}

// removeFiles deletes subdomain's data directory and its backup.
func (s *Service) removeFiles(ctx context.Context, subdomain string) error {
	return errors.Join(
		s.backups.Remove(ctx, DBFile(subdomain)),
		os.RemoveAll(filepath.Join(s.cfg.DataDir, subdomain)),
	)
}

// hasFiles reports whether subdomain's data directory exists, e.g. left
// behind by a deleted organization whose cleanup failed.
func (s *Service) hasFiles(subdomain string) bool {
	_, err := os.Stat(filepath.Join(s.cfg.DataDir, subdomain))
	return !errors.Is(err, fs.ErrNotExist)
}

// BackUpAll adds the database of every organization to backups, skipping
// missing ones.
func (s *Service) BackUpAll(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "s.BackUpAll()")

	orgs, err := s.queries.ListOrganizations(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, org := range orgs {
		if s.hasDatabase(org.Subdomain) {
			errs = append(errs, s.backups.Add(DBFile(org.Subdomain)))
		}
	}
	return errors.Join(errs...)
}

// Close stops every running headscale instance, closing their databases.
// Afterwards no instance starts again: starting one returns ErrNotRunning.
func (s *Service) Close(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "s.Close()")

	s.closed.Store(true)
	var errs []error
	for subdomain, rt := range s.orgs.Range {
		s.orgLock(subdomain).Lock()
		errs = append(errs, stopLocked(ctx, rt))
		s.orgLock(subdomain).Unlock()
	}
	return errors.Join(errs...)
}

// RunSleeper puts organizations whose headscale went unused for
// idleTimeout to sleep, checking every sweepInterval until ctx is done.
func (s *Service) RunSleeper(ctx context.Context) {
	t := time.NewTicker(s.sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sleepIdle(ctx)
		}
	}
}

// sleepIdle puts every running organization idle for idleTimeout to sleep.
func (s *Service) sleepIdle(ctx context.Context) {
	s.logger.Info("sweeping idle organizations")
	orgs, err := s.queries.ListOrganizations(ctx)
	if err != nil {
		s.logger.Warn("list organizations to sleep", "error", err)
		return
	}
	for _, org := range orgs {
		rt, ok := s.orgs.Load(org.Subdomain)
		if !ok || !rt.idle(time.Now(), s.idleTimeout) {
			continue
		}
		if _, ok := s.running(org.Subdomain); !ok {
			continue
		}
		sleepCtx, cancel := context.WithTimeout(ctx, sleepTimeout)
		err := s.ForOrg(org).Sleep(sleepCtx)
		cancel()
		if err != nil {
			s.logger.Warn("put idle organization to sleep", "subdomain", org.Subdomain, "error", err)
			continue
		}
		s.logger.Info("put idle organization to sleep", "subdomain", org.Subdomain)
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

// Organization statuses, as reported by Service.Status.
const (
	// StatusActive means the organization's headscale is serving.
	StatusActive = "active"
	// StatusUpdatePending means headscale is restarting to apply config.
	StatusUpdatePending = "update_pending"
	// StatusSleeping means its headscale isn't running because it went
	// unused or this process just started: it starts on next use.
	StatusSleeping = "sleeping"
	// StatusUnavailable means its headscale failed to start, stopped, or
	// lost its database.
	StatusUnavailable = "unavailable"
)
