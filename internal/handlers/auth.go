package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/auth/oidcprovider"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"

	"github.com/google/uuid"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
)

// BeginAuth kicks off the OAuth flow for the {provider} path segment.
func (h *Handlers) BeginAuth(w http.ResponseWriter, r *http.Request) {
	if next := r.URL.Query().Get("next"); next != "" {
		sess, err := gothic.Store.Get(r, gothic.SessionName)
		if err != nil {
			h.serverError(w, err)
			return
		}
		sess.Values["next"] = next
		if err := sess.Save(r, w); err != nil {
			h.serverError(w, err)
			return
		}
	}
	gothic.BeginAuthHandler(w, r)
}

// boolToInt64 converts a bool to SQLite's 0/1 integer representation, used
// for the users.email_verified column (sqlc models it as int64 since
// SQLite has no native boolean type).
func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// emailVerifiedFromGothUser extracts the upstream provider's "is this email
// verified" claim from the raw provider response. Google's userinfo v2
// endpoint (which goth's google provider uses) returns a "verified_email"
// boolean; other providers may use "email_verified" instead, so both are
// checked defensively.
func emailVerifiedFromGothUser(gothUser goth.User) bool {
	for _, key := range []string{"verified_email", "email_verified"} {
		if v, ok := gothUser.RawData[key].(bool); ok {
			return v
		}
	}
	return false
}

// AuthCallback completes the OAuth flow, finds or creates the local user
// record, stores the user id in the session, and redirects into the app
// (the SPA root, which loads GET /api/session client-side). First-time
// users without an organization yet are routed by the SPA itself to the
// organization creation form (see CreateOrganization).
func (h *Handlers) AuthCallback(w http.ResponseWriter, r *http.Request) {
	gothUser, err := gothic.CompleteUserAuth(w, r)
	if err != nil {
		h.serverError(w, err)
		return
	}

	ctx := r.Context()
	emailVerified := emailVerifiedFromGothUser(gothUser)
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
			EmailVerified:  boolToInt64(emailVerified),
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
			Email:         gothUser.Email,
			Name:          gothUser.Name,
			AvatarUrl:     gothUser.AvatarURL,
			EmailVerified: boolToInt64(emailVerified),
			ID:            user.ID,
		}); updErr != nil {
			h.logger.Error("update user profile", "error", updErr)
		}
	}

	if err := h.queries.TouchUserLastLogin(ctx, user.ID); err != nil {
		h.logger.Error("touch user last login", "error", err)
	}

	if err := auth.StoreUserSession(w, r, user.ID); err != nil {
		h.serverError(w, err)
		return
	}

	sess, err := gothic.Store.Get(r, gothic.SessionName)
	if err != nil {
		h.serverError(w, err)
		return
	}
	next, _ := sess.Values["next"].(string)
	delete(sess.Values, "next")
	if err := sess.Save(r, w); err != nil {
		h.serverError(w, err)
		return
	}
	if nextID := oidcprovider.AuthRequestIDFromNext(next); nextID != "" && h.oidc != nil {
		if err := h.oidc.AuthorizeAuthRequest(nextID, user.ID); err != nil {
			h.serverError(w, err)
			return
		}
	}
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
