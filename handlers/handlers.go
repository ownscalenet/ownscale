package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/auth/oidcprovider"
	"github.com/ownscalenet/ownscale/auth/passkey"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/orgs"
)

// headscaleClients reaches organizations' headscale instances. Implemented
// by *orgs.Service.
type headscaleClients interface {
	// Wake returns the instance's client, starting it first if it's asleep,
	// or orgs.ErrNotProvisioned for an organization whose headscale never
	// started.
	Wake(ctx context.Context, subdomain string) (*hscl.Client, error)
	// Ensure returns the client, starting the instance first if needed.
	Ensure(ctx context.Context, subdomain string) (*hscl.Client, error)
}

type Handlers struct {
	queries         *db.TxQueries
	orgs            *orgs.Service
	hs              headscaleClients
	baseDomain      string
	oidc            *oidcprovider.Storage
	logger          *slog.Logger
	funnel          *funnelproxy.Pool
	webauthn        *webauthn.WebAuthn
	passkeySessions *passkeyCeremonyStore
	authProviders   authProvidersStatus
}

// authProvidersStatus reports which upstream login providers are
// configured, so the login page can render each button accordingly (see
// AuthProviders).
type authProvidersStatus struct {
	OIDC      bool
	Google    bool
	Microsoft bool
}

// New builds the Handlers, wiring queries/baseDomain/dataDir into the
// orgs.Service that manages in-process headscale instances. dataDir is
// also used by the funnel proxy's per-organization tsnet.Server instances
// (see funnelproxy.NewPool); pass "" in tests that never exercise funnel
// routing. Returns an error if the WebAuthn relying party cannot be
// configured.
func New(
	queries *db.TxQueries,
	baseDomain, dataDir string,
	oidc *oidcprovider.Storage,
	logger *slog.Logger,
	derpCfg config.DerpConfig,
	authCfg config.AuthConfig) (_ *Handlers, err error) {
	defer derrors.Wrap(&err, "handlers.New(%q)", baseDomain)

	issuer := ""
	if oidc != nil {
		issuer = oidc.Issuer()
	}
	orgSvc := orgs.NewService(queries, baseDomain, issuer, derpCfg, logger, dataDir)

	// The console is served on console.baseDomain (per-org subdomains are
	// only used for headscale/tailnet traffic, not this admin UI), so one
	// static WebAuthn Relying Party ID/origin covers every organization.
	// Fall back to localhost for tests/dev that run without a configured
	// baseDomain; WebAuthn permits http://localhost as a secure context.
	rpID, rpOrigin := "", ""
	if baseDomain != "" {
		rpID = orgs.ConsoleDomain(baseDomain)
		rpOrigin = "https://" + rpID
	} else {
		rpID, rpOrigin = "localhost", "http://localhost"
	}
	wa, err := passkey.New(rpID, "Ownscale", rpOrigin)
	if err != nil {
		return nil, err
	}

	h := &Handlers{
		queries:         queries,
		baseDomain:      baseDomain,
		oidc:            oidc,
		logger:          logger,
		orgs:            orgSvc,
		hs:              orgSvc,
		funnel:          funnelproxy.NewPool(queries, orgSvc, dataDir, logger, orgSvc),
		webauthn:        wa,
		passkeySessions: newPasskeyCeremonyStore(),
		authProviders: authProvidersStatus{
			OIDC:      authCfg.UpstreamOIDC.Enabled(),
			Google:    authCfg.Google.Enabled(),
			Microsoft: authCfg.Microsoft.Enabled(),
		},
	}
	orgSvc.OnStart(h.linkAllMembers)
	orgSvc.OnSleep(h.funnel.Evict)
	return h, nil
}

// Close releases resources held by Handlers across the process's
// lifetime. Intended to be called once, at process shutdown.
func (h *Handlers) Close() {
	h.funnel.Close()
}

// ServeFunnelIfMatched serves r via the funnel proxy and returns true if
// r.Host is shaped like a funnel hostname ("<label>-<subdomain>.{base}",
// see etc/caddy/Caddyfile's "*.{$BASE_DOMAIN}" site block that routes
// these to ownscale, and funnelproxy.ParseHost); otherwise
// it does nothing and returns false, so the caller (server.NewMux) can
// fall through to the normal API/SPA mux.
func (h *Handlers) ServeFunnelIfMatched(w http.ResponseWriter, r *http.Request) bool {
	if h.baseDomain == "" {
		return false
	}
	label, subdomain, ok := funnelproxy.ParseHost(r.Host, h.baseDomain)
	if !ok {
		return false
	}
	h.funnel.ServeHTTP(w, r, label, subdomain)
	return true
}

// DialOrg connects to subdomain's in-process headscale instance. Used by
// server.NewSubdomainProxy to route subdomain traffic.
func (h *Handlers) DialOrg(ctx context.Context, subdomain string) (net.Conn, error) {
	return h.orgs.DialOrg(ctx, subdomain)
}

// Start runs background work, such as putting idle organizations to sleep,
// until ctx is done. It is non-blocking.
func (h *Handlers) Start(ctx context.Context) {
	go h.orgs.RunSleeper(ctx)
}

// writeJSON encodes v as the JSON response body with the given status code.
func (h *Handlers) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.logger.Error("encode json response", "error", err)
	}
}

// writeError writes a JSON error body {"error": msg} with the given status code.
func (h *Handlers) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}

func (h *Handlers) serverError(w http.ResponseWriter, r *http.Request, err error) {
	h.loggerFromContext(r.Context()).Error("request failed", "error", err)
	h.writeError(w, http.StatusInternalServerError, "internal server error")
}

type organizationResponse struct {
	Domain    string    `json:"Domain"`
	CreatedAt time.Time `json:"CreatedAt"`
	Status    string    `json:"Status"`
}

func (h *Handlers) toOrganizationResponse(org sqlcgen.Organization) organizationResponse {
	return organizationResponse{
		Domain:    org.Subdomain + "." + h.baseDomain,
		CreatedAt: org.CreatedAt,
		Status:    h.orgs.Status(org.Subdomain),
	}
}

// Session returns the currently authenticated user, plus their
// organization (including its provisioning status), as JSON. It is mounted
// behind auth.RequireAuthAPI, so reaching this handler implies a user is
// present in the request context.
func (h *Handlers) Session(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	org, err := h.queries.GetOrganization(r.Context(), user.OrganizationID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	linked := h.ensureCallerHeadscaleUser(r, *user, org)

	h.writeJSON(w, http.StatusOK, map[string]any{
		"user":         linked,
		"organization": h.toOrganizationResponse(org),
	})
}

// ensureCallerHeadscaleUser links user to a headscale user if it has none
// yet, and returns user with that link, so the response that made it
// already reports it.
func (h *Handlers) ensureCallerHeadscaleUser(r *http.Request, user sqlcgen.User, org sqlcgen.Organization) sqlcgen.User {
	if user.HeadscaleUserID.Valid {
		return user
	}
	logger := h.logger.With("org", org.Subdomain)
	client, _, notConfigured, err := h.resolveHeadscaleClient(r)
	if err != nil || notConfigured {
		return user
	}
	members, err := h.queries.ListUsersByOrganization(r.Context(), user.OrganizationID)
	if err != nil {
		logger.Warn("list organization members for headscale user linking", "error", err)
		return user
	}
	hsID, err := h.ensureHeadscaleUser(r.Context(), org.Subdomain, client, user, claimedHeadscaleUserIDs(members))
	if err != nil {
		logger.Warn("ensure headscale user", "user_id", user.ID, "error", err)
		return user
	}
	user.HeadscaleUserID = sql.NullString{String: hsID, Valid: true}
	return user
}
