package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/auth/oidcprovider"
	"github.com/ownscalenet/ownscale/auth/passkey"
	"github.com/ownscalenet/ownscale/backup"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/orgs"
	"github.com/ownscalenet/ownscale/utils"
)

// headscaleClients reaches organizations' headscale instances. Implemented
// by *orgs.Service.
type headscaleClients interface {
	// Ensure returns the client, starting the instance first if needed.
	Ensure(ctx context.Context, subdomain string) (*hscl.Client, error)
}

type Handlers struct {
	queries         *db.TxQueries
	orgs            *orgs.Service
	hs              headscaleClients
	baseDomain      string
	contentDomain   string
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
	GitHub    bool
}

// New builds the Handlers, wiring queries and cfg into the orgs.Service
// that manages in-process headscale instances. cfg.DataDir is also used by
// the funnel proxy's per-organization tsnet.Server instances (see
// funnelproxy.NewPool); pass "" in tests that never exercise funnel
// routing. backups replicates organizations' headscale databases and may be
// nil. Returns an error if the WebAuthn relying party cannot be configured.
func New(
	queries *db.TxQueries,
	cfg *config.Config,
	oidc *oidcprovider.Storage,
	logger *slog.Logger,
	backups *backup.Replicator) (_ *Handlers, err error) {
	defer derrors.Wrap(&err, "handlers.New(%q)", cfg.BaseDomain)

	baseDomain := cfg.BaseDomain

	issuer := ""
	if oidc != nil {
		issuer = oidc.Issuer()
	}
	orgSvc, err := orgs.NewService(queries, cfg, issuer, logger, backups)
	if err != nil {
		return nil, err
	}

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
		contentDomain:   cfg.ContentDomain,
		oidc:            oidc,
		logger:          logger,
		orgs:            orgSvc,
		hs:              orgSvc,
		funnel:          funnelproxy.NewPool(queries, orgSvc, cfg, logger),
		webauthn:        wa,
		passkeySessions: newPasskeyCeremonyStore(),
		authProviders: authProvidersStatus{
			OIDC:      cfg.Auth.UpstreamOIDC.Enabled(),
			Google:    cfg.Auth.Google.Enabled(),
			Microsoft: cfg.Auth.Microsoft.Enabled(),
			GitHub:    cfg.Auth.GitHub.Enabled(),
		},
	}
	orgSvc.OnSleep(h.funnel.Evict)
	return h, nil
}

// Close releases resources held by Handlers across the process's
// lifetime, stopping every organization's headscale. Intended to be called
// once, at process shutdown.
func (h *Handlers) Close(ctx context.Context) error {
	h.funnel.Close()
	return h.orgs.Close(ctx)
}

// ServeFunnelIfMatched serves r via the funnel proxy and returns true if
// r.Host is shaped like a funnel hostname ("<label>-<subdomain>.<contentDomain>",
// see etc/caddy/Caddyfile's "*.{$CONTENT_DOMAIN}" site address that routes
// these to ownscale, and funnelproxy.ParseHost); otherwise
// it does nothing and returns false, so the caller (server.NewMux) can
// fall through to the normal API/SPA mux.
func (h *Handlers) ServeFunnelIfMatched(w http.ResponseWriter, r *http.Request) bool {
	if h.contentDomain == "" {
		return false
	}
	label, subdomain, ok := funnelproxy.ParseHost(r.Host, h.contentDomain)
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

// Start backs up organizations' databases, then runs
// background work, such as putting idle organizations to sleep, until ctx
// is done. It doesn't wait for the background work.
func (h *Handlers) Start(ctx context.Context) {
	if err := h.orgs.BackUpAll(ctx); err != nil {
		h.logger.Error("back up organization databases", "error", err)
	}
	go h.orgs.RunSleeper(ctx)
	go h.orgs.RunDERPMapUpdater(ctx)
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
	h.writeJSON(w, http.StatusOK, map[string]any{
		"user":         user,
		"organization": h.toOrganizationResponse(org),
		"version":      utils.Version,
	})
}
