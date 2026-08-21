package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/orgs"

	"github.com/google/uuid"
	"github.com/markbates/goth/gothic"
)

// BeginAuth kicks off the OAuth flow for the {provider} path segment.
func (h *Handlers) BeginAuth(w http.ResponseWriter, r *http.Request) {
	gothic.BeginAuthHandler(w, r)
}

// AuthCallback completes the OAuth flow, finds or creates the local user
// record, auto-provisions a fresh organization for first-time logins, stores
// the user id in the session, and redirects into the app (the SPA root,
// which loads GET /api/session client-side).
func (h *Handlers) AuthCallback(w http.ResponseWriter, r *http.Request) {
	gothUser, err := gothic.CompleteUserAuth(w, r)
	if err != nil {
		h.serverError(w, err)
		return
	}

	ctx := r.Context()
	user, err := h.queries.GetUserByProvider(ctx, sqlcgen.GetUserByProviderParams{
		Provider:       gothUser.Provider,
		ProviderUserID: gothUser.UserID,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		user, err = h.queries.CreateUser(ctx, sqlcgen.CreateUserParams{
			ID:             uuid.Must(uuid.NewV7()).String(),
			Provider:       gothUser.Provider,
			ProviderUserID: gothUser.UserID,
			Email:          gothUser.Email,
			Name:           gothUser.Name,
			AvatarUrl:      gothUser.AvatarURL,
		})
		if err != nil {
			h.serverError(w, err)
			return
		}
	case err != nil:
		h.serverError(w, err)
		return
	default:
		// Existing user: keep profile fields fresh.
		if updErr := h.queries.UpdateUserProfile(ctx, sqlcgen.UpdateUserProfileParams{
			Email:     gothUser.Email,
			Name:      gothUser.Name,
			AvatarUrl: gothUser.AvatarURL,
			ID:        user.ID,
		}); updErr != nil {
			h.logger.Error("update user profile", "error", updErr)
		}
	}

	if !user.OrganizationID.Valid {
		org, err := h.provisionOrganization(ctx)
		if err != nil {
			h.serverError(w, err)
			return
		}
		if err := h.queries.SetUserOrganization(ctx, sqlcgen.SetUserOrganizationParams{
			OrganizationID: sql.NullString{String: org.ID, Valid: true},
			ID:             user.ID,
		}); err != nil {
			h.serverError(w, err)
			return
		}
	}

	if err := auth.StoreUserSession(w, r, user.ID); err != nil {
		h.serverError(w, err)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// provisionOrganization creates a brand-new organization with a random
// subdomain (DB row + headscale container), retrying a couple of times on
// the unlikely event of a subdomain collision.
func (h *Handlers) provisionOrganization(ctx context.Context) (sqlcgen.Organization, error) {
	if h.orgs == nil {
		return sqlcgen.Organization{}, errors.New("organization auto-provisioning is not configured (BASE_DOMAIN/Docker unavailable)")
	}

	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		subdomain, err := orgs.RandomSubdomain()
		if err != nil {
			return sqlcgen.Organization{}, err
		}
		org, err := h.orgs.Create(ctx, subdomain)
		if err == nil {
			return org, nil
		}
		lastErr = err
	}
	return sqlcgen.Organization{}, lastErr
}

// Logout clears the session and returns to the login page. Kept as a plain
// GET redirect for non-JS fallback links.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if err := auth.ClearUserSession(w, r); err != nil {
		h.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// APILogout clears the session and returns 204 No Content, for use by the
// SPA (which then redirects client-side to /login).
func (h *Handlers) APILogout(w http.ResponseWriter, r *http.Request) {
	if err := auth.ClearUserSession(w, r); err != nil {
		h.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
