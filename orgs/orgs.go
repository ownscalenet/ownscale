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
	// shares one non-thread-safe rand.Rand across instances). This is not
	// enough to prevent races, there's still a race because the derp worker
	// (usually at 3h interval) calls GetDERPMap, which again touches the
	// global rand.Rand.
	startMu sync.Mutex

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
	client *hscl.Client
	cancel context.CancelFunc
	// exited is closed once app.Serve has returned err.
	exited chan struct{}
	err    error
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
func (s *Service) Client(subdomain string) (*hscl.Client, error) {
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
// subdomain, registering it in s.orgs. It listens on a loopback port
// picked by the kernel, reused across restarts of the instance within this
// process; if another process took that port, it retries on a new one.
//
// Instances start one at a time: this returns once the instance serves,
// or with the reason it doesn't.
func (s *Service) startHeadscale(ctx context.Context, subdomain string, hsCfg *HeadscaleConfig) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	return s.startLocked(ctx, subdomain, hsCfg)
}

// startLocked is startHeadscale for callers already holding startMu.
func (s *Service) startLocked(ctx context.Context, subdomain string, hsCfg *HeadscaleConfig) error {
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
	app, err := s.createFn(s.dataDir, subdomain, s.baseDomain, port, hsCfg, s.derpCfg)
	if err != nil {
		return nil, fmt.Errorf("create headscale instance for %q: %w", subdomain, err)
	}
	if app == nil {
		return nil, nil
	}

	if _, err := app.GetState().GetPolicy(); err != nil {
		if _, err := app.GetState().SetPolicyInDB(defaultACLPolicy); err != nil {
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
