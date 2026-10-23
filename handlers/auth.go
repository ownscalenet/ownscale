package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/auth/oidcprovider"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"

	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/rs/xid"
)

// nextCookieName is a short-lived, single-purpose cookie used to remember
// where to redirect the user after OAuth login completes. It is
// intentionally kept separate from gothic's own session cookie: gothic's
// BeginAuthHandler re-reads/re-saves its session from the original request
// as part of starting the provider auth (see GetAuthURL/StoreInSession in
// goth/gothic), which would silently clobber a "next" value stored in that
// same session by BeginAuth just beforehand, since the incoming request
// object never reflects Set-Cookie headers written earlier in the same
// response.
const nextCookieName = "__Host-ownscale_next"

// nextCookieMaxAge is deliberately short: the cookie only needs to survive
// the redirect to the upstream IdP and back.
const nextCookieMaxAge = 10 * 60 // 10 minutes, in seconds

func isInviteAcceptPath(next string) bool {
	return strings.HasPrefix(next, "/invite/")
}

// inviteTokenFromNext extracts the invite token from a next path of the
// form "/invite/{token}" (see AcceptInvite.tsx's /login?next=... redirect).
func inviteTokenFromNext(next string) (token string, ok bool) {
	if !isInviteAcceptPath(next) {
		return "", false
	}
	token = strings.TrimPrefix(next, "/invite/")
	if token == "" {
		return "", false
	}
	return token, true
}

type inviteInvalidError struct{ msg string }

func (e *inviteInvalidError) Error() string { return e.msg }

// errUsernameTaken means a passkey sign-up chose a username already used by
// another passkey account in the same organization.
var errUsernameTaken = errors.New("username is already taken in this organization")

// createNewUser creates a brand-new user row, always with a resolved
// organization and role: either the invite's organization+role (when next
// points at an invite-accept path, consuming the invite atomically with
// user creation), or a freshly created organization with the owner role.
func (h *Handlers) createNewUser(ctx context.Context, params sqlcgen.CreateUserParams, next string) (_ sqlcgen.User, err error) {
	defer derrors.Wrap(&err, "h.createNewUser(%q)", next)

	token, isInvite := inviteTokenFromNext(next)
	if !isInvite {
		subdomain, err := h.orgs.NextSubdomain(ctx)
		if err != nil {
			return sqlcgen.User{}, err
		}
		org, err := h.orgs.Create(ctx, subdomain)
		if err != nil {
			return sqlcgen.User{}, err
		}
		params.OrganizationID = org.ID
		params.Role = RoleOwner
		return h.queries.CreateUser(ctx, params)
	}

	inv, err := h.queries.GetInviteByToken(ctx, token)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.User{}, &inviteInvalidError{"invite not found"}
	}
	if err != nil {
		return sqlcgen.User{}, err
	}
	if inv.AcceptedAt.Valid {
		return sqlcgen.User{}, &inviteInvalidError{"invite has already been used"}
	}
	if inv.ExpiresAt.Before(time.Now()) {
		return sqlcgen.User{}, &inviteInvalidError{"invite has expired"}
	}
	params.OrganizationID = inv.OrganizationID
	params.Role = inv.Role

	txQueries, commit, rollback, err := h.queries.StartTx(ctx)
	if err != nil {
		return sqlcgen.User{}, err
	}
	defer rollback()

	// Passkey usernames (stored as username@passkey emails) must be unique
	// within the organization. A new-org signup can't collide (its org is
	// empty), so only the invite path needs this, in the same tx as the
	// insert.
	if params.Provider == passkeyProvider {
		taken, err := txQueries.CountOrgProviderEmail(ctx, sqlcgen.CountOrgProviderEmailParams{
			OrganizationID: params.OrganizationID,
			Provider:       passkeyProvider,
			Email:          params.Email,
		})
		if err != nil {
			return sqlcgen.User{}, err
		}
		if taken > 0 {
			return sqlcgen.User{}, errUsernameTaken
		}
	}

	// The user row must exist before accepted_by_user_id can reference
	// it, so create the user first.
	user, err := txQueries.CreateUser(ctx, params)
	if err != nil {
		return sqlcgen.User{}, err
	}

	n, err := txQueries.MarkInviteAcceptedIfPending(ctx, sqlcgen.MarkInviteAcceptedIfPendingParams{
		AcceptedByUserID: sql.NullString{String: params.ID, Valid: true},
		ID:               inv.ID,
	})
	if err != nil {
		return sqlcgen.User{}, err
	}
	if n == 0 {
		return sqlcgen.User{}, &inviteInvalidError{"invite has already been used"}
	}
	if err := commit(); err != nil {
		return sqlcgen.User{}, err
	}
	return user, nil
}

// AuthProviders reports which upstream login providers are configured, so
// the login page can render each button accordingly (greyed out with an
// explanatory tooltip when disabled, rather than a dead link). Public:
// served before login.
func (h *Handlers) AuthProviders(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]bool{
		"oidc":      h.authProviders.OIDC,
		"google":    h.authProviders.Google,
		"microsoft": h.authProviders.Microsoft,
	})
}

// BeginAuth kicks off the OAuth flow for the {provider} path segment.
func (h *Handlers) BeginAuth(w http.ResponseWriter, r *http.Request) {
	if next := r.URL.Query().Get("next"); next != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     nextCookieName,
			Value:    next,
			Path:     "/",
			MaxAge:   nextCookieMaxAge,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteNoneMode,
		})
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

// This only works for google, not microsoft.
func emailVerifiedFromGothUser(gothUser goth.User) bool {
	for _, key := range []string{"verified_email", "email_verified"} {
		if v, ok := gothUser.RawData[key].(bool); ok {
			return v
		}
	}
	return false
}

func (h *Handlers) AuthCallback(w http.ResponseWriter, r *http.Request) {
	gothUser, err := gothic.CompleteUserAuth(w, r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	var next string
	if c, err := r.Cookie(nextCookieName); err == nil {
		next = c.Value
	}
	http.SetCookie(w, &http.Cookie{
		Name:     nextCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	})

	ctx := r.Context()
	emailVerified := emailVerifiedFromGothUser(gothUser)
	user, err := h.queries.GetUserByProvider(ctx, sqlcgen.GetUserByProviderParams{
		Provider:       gothUser.Provider,
		ProviderUserID: gothUser.UserID,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		user, err = h.createNewUser(ctx, sqlcgen.CreateUserParams{
			ID:             xid.New().String(),
			Provider:       gothUser.Provider,
			ProviderUserID: gothUser.UserID,
			Email:          gothUser.Email,
			Name:           gothUser.Name,
			AvatarUrl:      gothUser.AvatarURL,
			EmailVerified:  boolToInt64(emailVerified),
		}, next)
		if err != nil {
			if invErr, ok := errors.AsType[*inviteInvalidError](err); ok {
				h.writeError(w, http.StatusGone, invErr.Error())
				return
			}
			h.serverError(w, r, err)
			return
		}
	case err != nil:
		h.serverError(w, r, err)
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

	h.finishLogin(w, r, user, next)
}

// finishLogin completes a login/registration for user, regardless of which
// auth method produced it: OAuth callback, passkey registration, or passkey login.
func (h *Handlers) finishLogin(w http.ResponseWriter, r *http.Request, user sqlcgen.User, next string) {
	if err := auth.StoreUserSession(w, r, user.ID); err != nil {
		h.serverError(w, r, err)
		return
	}
	if nextID := oidcprovider.AuthRequestIDFromNext(next); nextID != "" && h.oidc != nil {
		if err := h.oidc.AuthorizeAuthRequest(r.Context(), nextID, user.ID); errors.Is(err, oidcprovider.ErrNotMember) {
			http.Error(w, "You aren't a member of this organization. Ask its owner for an invite.", http.StatusForbidden)
			return
		} else if err != nil {
			h.serverError(w, r, err)
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
		h.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// APILogout clears the session and returns 204 No Content, for use by the
// SPA (which then redirects client-side to /login).
func (h *Handlers) APILogout(w http.ResponseWriter, r *http.Request) {
	if err := auth.ClearUserSession(w, r); err != nil {
		h.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
