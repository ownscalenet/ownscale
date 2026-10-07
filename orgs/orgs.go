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
	"net/http"
	"os"
	"slices"
	"strconv"
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

// fixedSubdomains are handed out in order to the first organizations
// created, so development identities land on predictable subdomains.
// TODO: move these to env.
var fixedSubdomains = []string{
	"aaaa", "bbbb", "cccc", "dddd", "eeee",
	"ffff", "gggg", "hhhh", "iiii", "jjjj",
}

// Service provisions organizations and manages the lifecycle of every
// org's in-process hscontrol.Headscale instance.
type Service struct {
	queries    *db.TxQueries
	baseDomain string
	issuer     string
	derpCfg    config.DerpConfig
	logger     *slog.Logger
	dataDir    string
	backups    *backup.Replicator

	orgs utils.Map[string, *orgRuntime]
	// derpMap is the last DERP map RunDERPMapUpdater fetched.
	derpMap atomic.Pointer[tailcfg.DERPMap]
	// restarts counts each org's in-flight restarts, including those still
	// waiting on startMu; a counter as two settings saves can overlap.
	restarts utils.Map[string, *atomic.Int32]
	// startFailures holds when Ensure last failed to start each org, so
	// device traffic can't retry a broken org on every request.
	startFailures utils.Map[string, time.Time]
	// onStart, if set, runs once Ensure started an org's headscale.
	onStart func(ctx context.Context, subdomain string, client *hscl.Client)
	// onSleep, if set, runs once an org's headscale was put to sleep.
	onSleep func(subdomain string)

	// idleTimeout and sweepInterval are overridden in tests.
	idleTimeout   time.Duration
	sweepInterval time.Duration

	// startMu serializes headscale startup and DERP map pushes, as
	// headscale's DERP map shuffling shares one non-thread-safe rand.Rand.
	startMu sync.Mutex

	// createFn defaults to createHeadscale; override in tests to skip
	// real headscale startup.
	createFn func(dataDir, subdomain, baseDomain string, port int, hsCfg *HeadscaleConfig, derpMap *tailcfg.DERPMap) (*hscontrol.Headscale, error)
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

// NewService builds a Service that manages organizations under baseDomain.
// Each organization's headscale database is added to backups once it
// exists; backups may be nil.
func NewService(queries *db.TxQueries, baseDomain, issuer string,
	derpCfg config.DerpConfig, logger *slog.Logger,
	dataDir string, backups *backup.Replicator) *Service {
	return &Service{
		queries:       queries,
		baseDomain:    baseDomain,
		issuer:        issuer,
		derpCfg:       derpCfg,
		logger:        logger,
		dataDir:       dataDir,
		backups:       backups,
		createFn:      createHeadscale,
		idleTimeout:   idleTimeout,
		sweepInterval: sweepInterval,
	}
}

// OnStart sets f to run each time Ensure starts an organization's
// headscale, before Ensure returns. It must be set before the Service is
// used.
func (s *Service) OnStart(f func(ctx context.Context, subdomain string, client *hscl.Client)) {
	s.onStart = f
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
		restart: func(ctx context.Context) error {
			return s.restartHeadscale(ctx, sub)
		},
		sleep: func(ctx context.Context) error {
			return s.sleepHeadscale(ctx, sub)
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

// provisioned reports whether subdomain's headscale was ever started: in
// this process, or before it, which left headscale's database behind.
func (s *Service) provisioned(subdomain string) bool {
	if _, ok := s.orgs.Load(subdomain); ok {
		return true
	}
	_, err := os.Stat(sqlitePath(s.dataDir, subdomain))
	return err == nil
}

// Status reports the state of subdomain's headscale instance: one of
// StatusUpdatePending, StatusActive, StatusUnprovisioned, StatusSleeping or
// StatusUnavailable.
func (s *Service) Status(subdomain string) string {
	if n, ok := s.restarts.Load(subdomain); ok && n.Load() > 0 {
		return StatusUpdatePending
	}
	if _, ok := s.running(subdomain); ok {
		return StatusActive
	}
	if !s.provisioned(subdomain) {
		return StatusUnprovisioned
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

// Client returns a client for subdomain's running headscale instance. It
// returns ErrNotProvisioned if the instance never started.
func (s *Service) Client(subdomain string) (*hscl.Client, error) {
	inst, ok := s.running(subdomain)
	if !ok {
		if !s.provisioned(subdomain) {
			return nil, ErrNotProvisioned
		}
		return nil, ErrNotRunning
	}
	s.touch(subdomain)
	return inst.client, nil
}

// Wake returns a client for subdomain's headscale instance, starting it
// first if it's asleep (see Ensure). It returns ErrNotProvisioned if the
// instance never started.
func (s *Service) Wake(ctx context.Context, subdomain string) (_ *hscl.Client, err error) {
	defer derrors.Wrap(&err, "s.Wake(%q)", subdomain)

	if !s.provisioned(subdomain) {
		return nil, ErrNotProvisioned
	}
	return s.Ensure(ctx, subdomain)
}

var (
	// ErrNotRunning is returned for an organization without a running
	// headscale instance.
	ErrNotRunning = errors.New("organization not running")
	// ErrNotProvisioned is returned for an organization whose headscale
	// instance never started.
	ErrNotProvisioned = errors.New("organization not provisioned")
)

// startRetryDelay is how long Ensure fails fast after it failed to start
// an organization.
const startRetryDelay = 30 * time.Second

// Ensure returns a client for subdomain's headscale instance, starting it
// first if it isn't running, which provisions an organization that never
// ran. For startRetryDelay after a failed start it returns ErrNotRunning
// without trying again. An unknown subdomain gets ErrNotRunning.
func (s *Service) Ensure(ctx context.Context, subdomain string) (_ *hscl.Client, err error) {
	defer derrors.Wrap(&err, "s.Ensure(%q)", subdomain)

	if inst, ok := s.running(subdomain); ok {
		s.touch(subdomain)
		return inst.client, nil
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
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
	if err := s.startLocked(ctx, org.ID, subdomain, hsCfg); err != nil {
		s.startFailures.Store(subdomain, time.Now())
		return nil, err
	}
	s.startFailures.Delete(subdomain)
	inst, ok := s.running(subdomain)
	if !ok {
		return nil, ErrNotRunning // createFn built no instance (tests)
	}
	if s.onStart != nil {
		s.onStart(ctx, subdomain, inst.client)
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
// identity headscale derives from an ownscale login of the user subject.
// Without it, that person's first OIDC login through headscale creates a
// second headscale user, as headscale only matches existing users by this
// identity. An identity the user already has is replaced: it belongs to an
// ownscale account that no longer owns this headscale user, such as a
// removed member who was invited back. Identities already owned by another
// user are left alone.
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

// Create stores a new organization with the given subdomain. Its headscale
// instance starts later, on first use (see Ensure).
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

	return s.insertProvisioned(ctx, subdomain, clientID, string(clientSecretHash), string(redirectURIsJSON), hsConfig)
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
// Callers hold startMu, so instances start one at a time.
//
// It returns once the instance serves, or with the reason it doesn't.
func (s *Service) startLocked(ctx context.Context, orgID, subdomain string, hsCfg *HeadscaleConfig) error {
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
		inst, err := s.launch(ctx, subdomain, port, hsCfg)
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
func (s *Service) launch(ctx context.Context, subdomain string, port int, hsCfg *HeadscaleConfig) (*instance, error) {
	derpMap, err := s.currentDERPMap(ctx)
	if err != nil {
		return nil, err
	}
	app, err := s.createFn(s.dataDir, subdomain, s.baseDomain, port, hsCfg, derpMap)
	if err != nil {
		return nil, fmt.Errorf("create headscale instance for %q: %w", subdomain, err)
	}
	if app == nil {
		return nil, nil
	}

	if _, err := app.GetState().GetPolicy(); err != nil {
		if _, err := app.GetState().SetPolicyInDB(DefaultACLPolicy); err != nil {
			s.logger.Warn("seed default ACL policy", "subdomain", subdomain, "error", err)
		}
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	inst := &instance{
		app:    app,
		client: hscl.NewUnix(SocketPath(s.dataDir, subdomain)),
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
// port. It starts a provisioned instance that isn't running, and leaves an
// unprovisioned organization alone.
func (s *Service) restartHeadscale(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "s.restartHeadscale(%q)", subdomain)

	n, _ := s.restarts.LoadOrStore(subdomain, new(atomic.Int32))
	n.Add(1)
	defer n.Add(-1)

	s.startMu.Lock()
	defer s.startMu.Unlock()

	// The stored config takes effect on the first start.
	if !s.provisioned(subdomain) {
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
// connections. Callers hold startMu.
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
	s.startMu.Lock()
	defer s.startMu.Unlock()

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

// BackUpProvisioned adds the database of every organization whose
// headscale ever started to backups.
func (s *Service) BackUpProvisioned(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "s.BackUpProvisioned()")

	orgs, err := s.queries.ListOrganizations(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, org := range orgs {
		if s.provisioned(org.Subdomain) {
			errs = append(errs, s.backups.Add(DBFile(org.Subdomain)))
		}
	}
	return errors.Join(errs...)
}

// Close stops every running headscale instance, closing their databases.
func (s *Service) Close(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "s.Close()")

	s.startMu.Lock()
	defer s.startMu.Unlock()
	var errs []error
	for _, rt := range s.orgs.Range {
		errs = append(errs, stopLocked(ctx, rt))
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
	// StatusUpdatePending means its headscale is restarting to apply config.
	StatusUpdatePending = "update_pending"
	// StatusUnprovisioned means its headscale never started: it starts on
	// first use.
	StatusUnprovisioned = "unprovisioned"
	// StatusSleeping means its headscale isn't running because it went
	// unused or this process just started: it starts on next use.
	StatusSleeping = "sleeping"
	// StatusUnavailable means its headscale failed to start or stopped.
	StatusUnavailable = "unavailable"
)
