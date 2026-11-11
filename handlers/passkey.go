package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/auth/passkey"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/orgs"
)

const passkeyProvider = "passkey"
const defaultPasskeyName = "Passkey"

// passkeyCredentialSummary is the JSON shape returned for a stored
// credential; skipping the public key and raw credential ID is intentional.
type passkeyCredentialSummary struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	CreatedAt  string  `json:"createdAt"`
	LastUsedAt *string `json:"lastUsedAt"`
}

func toPasskeyCredentialSummary(c sqlcgen.WebauthnCredential) passkeyCredentialSummary {
	s := passkeyCredentialSummary{
		ID:        c.ID,
		Name:      c.Name,
		CreatedAt: c.CreatedAt.Format(time.RFC3339),
	}
	if c.LastUsedAt.Valid {
		v := c.LastUsedAt.Time.Format(time.RFC3339)
		s.LastUsedAt = &v
	}
	return s
}

// setPasskeyCeremonyCookie stores tok as the ceremony cookie, scoped to the
// short lifetime of a single WebAuthn ceremony.
func setPasskeyCeremonyCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name:     passkeyCeremonyCookie,
		Value:    tok,
		Path:     "/",
		MaxAge:   int(passkeyCeremonyTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearPasskeyCeremonyCookie deletes the ceremony cookie once a ceremony
// has been consumed (successfully or not).
func clearPasskeyCeremonyCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     passkeyCeremonyCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// takePasskeyCeremony reads the ceremony cookie from r and pops the
// matching ceremony from the store, writing a 400 response and returning
// false if it's missing or expired.
func (h *Handlers) takePasskeyCeremony(w http.ResponseWriter, r *http.Request) (passkeyCeremony, bool) {
	c, err := r.Cookie(passkeyCeremonyCookie)
	if err != nil || c.Value == "" {
		h.writeError(w, http.StatusBadRequest, "no passkey ceremony in progress")
		return passkeyCeremony{}, false
	}
	ceremony, ok := h.passkeySessions.take(c.Value)
	if !ok {
		h.writeError(w, http.StatusBadRequest, "passkey ceremony expired, please try again")
		return passkeyCeremony{}, false
	}
	return ceremony, true
}

// passkeyEmailDomain is the synthetic domain passkey accounts' emails live
// in. It's not a real email domain, so passkey usernames don't collide with
// SSO users' emails.
const passkeyEmailDomain = "passkey"

func passkeyEmail(username string) string {
	return username + "@" + passkeyEmailDomain
}

// It's a subset of headscale's util.ValidateUsername so the derived headscale
// user name is always valid.
var validPasskeyUsername = regexp.MustCompile(`^[a-z][a-z0-9._-]{1,31}$`)

// reservedPasskeyUsernames can't be chosen, as they name headscale users
// ownscale manages itself.
var reservedPasskeyUsernames = map[string]bool{
	funnelproxy.AgentHeadscaleUserName: true,
	"devbox":                           true,
	"user":                             true,
}

// validatePasskeyUsername normalizes and checks username, returning the
// normalized form or a message explaining why it's rejected.
func validatePasskeyUsername(username string) (string, string) {
	u := strings.ToLower(strings.TrimSpace(username))
	if !validPasskeyUsername.MatchString(u) {
		return "", "username must be 2-32 characters, start with a letter, and use only letters, digits, '.', '-' or '_'"
	}
	if reservedPasskeyUsernames[u] {
		return "", "that username is reserved"
	}
	return u, ""
}

// requirePasskeyEnabled writes a 403 and returns false if passkey auth is
// disabled (OWNSCALE_AUTH_PASSKEY_DISABLED), so every passkey endpoint
// rejects requests the same way regardless of how a client reached it.
func (h *Handlers) requirePasskeyEnabled(w http.ResponseWriter) bool {
	if !h.authProviders.Passkey {
		h.writeError(w, http.StatusForbidden, "passkey auth is disabled for this deployment")
		return false
	}
	return true
}

// PasskeyRegisterBegin is the first step of self-service account creation via passkey.
func (h *Handlers) PasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if !h.requirePasskeyEnabled(w) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Next     string `json:"next"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	username, msg := validatePasskeyUsername(req.Username)
	if msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	email := passkeyEmail(username)

	pendingID := xid.New().String()
	user := passkey.User{Row: sqlcgen.User{ID: pendingID, Name: username, Email: email}}

	creation, session, err := h.webauthn.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired))
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	tok, err := h.passkeySessions.save(passkeyCeremony{
		Session:       session,
		Username:      username,
		Email:         email,
		PendingUserID: pendingID,
		Name:          defaultPasskeyName,
		Next:          req.Next,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	setPasskeyCeremonyCookie(w, tok)
	h.writeJSON(w, http.StatusOK, creation)
}

// PasskeyRegisterFinish verifies the browser's attestation, creates the new
// user + credential, and finishes the login the same way AuthCallback does.
func (h *Handlers) PasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if !h.requirePasskeyEnabled(w) {
		return
	}
	ceremony, ok := h.takePasskeyCeremony(w, r)
	clearPasskeyCeremonyCookie(w)
	if !ok {
		return
	}
	if ceremony.PendingUserID == "" {
		h.writeError(w, http.StatusBadRequest, "no registration ceremony in progress")
		return
	}

	pendingUser := passkey.User{Row: sqlcgen.User{ID: ceremony.PendingUserID, Name: ceremony.Username, Email: ceremony.Email}}
	cred, err := h.webauthn.FinishRegistration(pendingUser, *ceremony.Session, r)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "could not verify passkey: "+err.Error())
		return
	}

	ctx := r.Context()
	user, err := h.createNewUser(ctx, sqlcgen.CreateUserParams{
		ID:             ceremony.PendingUserID,
		Provider:       passkeyProvider,
		ProviderUserID: ceremony.PendingUserID,
		Email:          ceremony.Email,
		Name:           ceremony.Username,
		AvatarUrl:      "",
		EmailVerified:  0,
	}, ceremony.Next)
	if err != nil {
		if invErr, ok := errors.AsType[*inviteInvalidError](err); ok {
			h.writeError(w, http.StatusGone, invErr.Error())
			return
		}
		if errors.Is(err, orgs.ErrMaxOrgs) {
			h.writeError(w, http.StatusForbidden, errMaxOrgsMessage)
			return
		}
		if errors.Is(err, errHeadscaleUnavailable) {
			h.logger.Warn("sign-up: create headscale user", "error", err)
			h.writeError(w, http.StatusServiceUnavailable, "headscale is unavailable right now, try again in a moment")
			return
		}
		if errors.Is(err, errUsernameTaken) {
			h.writeError(w, http.StatusConflict, "that username is already taken in this organization")
			return
		}
		h.serverError(w, r, err)
		return
	}
	if _, err := h.queries.CreateWebauthnCredential(ctx, passkey.CredentialParams(xid.New().String(), user.ID, ceremony.Name, *cred)); err != nil {
		h.serverError(w, r, err)
		return
	}
	if err := h.queries.TouchUserLastLogin(ctx, user.ID); err != nil {
		h.logger.Error("touch user last login", "error", err)
	}

	h.finishLogin(w, r, user, ceremony.Next)
}

// PasskeyLoginBegin a single "sign in with passkey" button with no email/username
// typed first.
func (h *Handlers) PasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !h.requirePasskeyEnabled(w) {
		return
	}
	var req struct {
		Next string `json:"next"`
	}
	// The request body is optional (a bare click); ignore decode failures
	// for an empty body.
	_ = h.decodeJSONBodyOptional(r, &req)

	assertion, session, err := h.webauthn.BeginDiscoverableLogin()
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	tok, err := h.passkeySessions.save(passkeyCeremony{Session: session, Next: req.Next})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	setPasskeyCeremonyCookie(w, tok)
	h.writeJSON(w, http.StatusOK, assertion)
}

// PasskeyLoginFinish verifies the assertion against whichever credential/user
// the authenticator reports, bumps the credential's sign count, and finishes the login.
func (h *Handlers) PasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if !h.requirePasskeyEnabled(w) {
		return
	}
	ceremony, ok := h.takePasskeyCeremony(w, r)
	clearPasskeyCeremonyCookie(w)
	if !ok {
		return
	}

	ctx := r.Context()
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		userID := string(userHandle)
		u, err := h.queries.GetUserByID(ctx, userID)
		if err != nil {
			return nil, err
		}
		creds, err := h.queries.ListWebauthnCredentialsByUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		return passkey.User{Row: u, Credentials: creds}, nil
	}

	validatedUser, cred, err := h.webauthn.FinishPasskeyLogin(handler, *ceremony.Session, r)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "could not verify passkey: "+err.Error())
		return
	}
	pu, ok := validatedUser.(passkey.User)
	if !ok {
		h.serverError(w, r, errors.New("unexpected webauthn.User implementation"))
		return
	}

	if row, err := h.queries.GetWebauthnCredentialByCredentialID(ctx, cred.ID); err != nil {
		h.logger.Error("look up webauthn credential after login", "error", err)
	} else if err := h.queries.UpdateWebauthnCredentialSignCount(ctx, sqlcgen.UpdateWebauthnCredentialSignCountParams{
		SignCount: int64(cred.Authenticator.SignCount),
		ID:        row.ID,
	}); err != nil {
		h.logger.Error("update webauthn credential sign count", "error", err)
	}
	if err := h.queries.TouchUserLastLogin(ctx, pu.Row.ID); err != nil {
		h.logger.Error("touch user last login", "error", err)
	}

	h.finishLogin(w, r, pu.Row, ceremony.Next)
}

func (h *Handlers) ListPasskeys(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	rows, err := h.queries.ListWebauthnCredentialsByUser(r.Context(), user.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	out := make([]passkeyCredentialSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPasskeyCredentialSummary(row))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"passkeys": out})
}

// PasskeyAddBegin lets an already-authenticated user (created via OAuth or passkey)
// register an additional passkey as a login method.
func (h *Handlers) PasskeyAddBegin(w http.ResponseWriter, r *http.Request) {
	if !h.requirePasskeyEnabled(w) {
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = h.decodeJSONBodyOptional(r, &req)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = defaultPasskeyName
	}

	existing, err := h.queries.ListWebauthnCredentialsByUser(r.Context(), user.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	pu := passkey.User{Row: *user, Credentials: existing}
	excludeList := make([]protocol.CredentialDescriptor, 0, len(existing))
	for _, c := range pu.WebAuthnCredentials() {
		excludeList = append(excludeList, c.Descriptor())
	}

	creation, session, err := h.webauthn.BeginRegistration(pu,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(excludeList))
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	tok, err := h.passkeySessions.save(passkeyCeremony{Session: session, UserID: user.ID, Name: name})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	setPasskeyCeremonyCookie(w, tok)
	h.writeJSON(w, http.StatusOK, creation)
}

// PasskeyAddFinish completes adding an extra passkey for the already-authenticated caller.
func (h *Handlers) PasskeyAddFinish(w http.ResponseWriter, r *http.Request) {
	if !h.requirePasskeyEnabled(w) {
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	ceremony, ok := h.takePasskeyCeremony(w, r)
	clearPasskeyCeremonyCookie(w)
	if !ok {
		return
	}
	if ceremony.UserID != user.ID {
		h.writeError(w, http.StatusBadRequest, "passkey ceremony does not belong to the current session")
		return
	}

	ctx := r.Context()
	existing, err := h.queries.ListWebauthnCredentialsByUser(ctx, user.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	pu := passkey.User{Row: *user, Credentials: existing}
	cred, err := h.webauthn.FinishRegistration(pu, *ceremony.Session, r)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "could not verify passkey: "+err.Error())
		return
	}
	row, err := h.queries.CreateWebauthnCredential(ctx, passkey.CredentialParams(xid.New().String(), user.ID, ceremony.Name, *cred))
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, map[string]any{"passkey": toPasskeyCredentialSummary(row)})
}

// DeletePasskey removes one of the caller's own credentials.
func (h *Handlers) DeletePasskey(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id := r.PathValue("id")
	row, err := h.queries.GetWebauthnCredential(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.writeError(w, http.StatusNotFound, "passkey not found")
			return
		}
		h.serverError(w, r, err)
		return
	}
	if row.UserID != user.ID {
		h.writeError(w, http.StatusNotFound, "passkey not found")
		return
	}
	// Prevent users locking themselves up by deleting their only auth option.
	if user.Provider == passkeyProvider {
		n, err := h.queries.DeleteWebauthnCredentialUnlessLast(r.Context(), sqlcgen.DeleteWebauthnCredentialUnlessLastParams{
			ID:     id,
			UserID: user.ID,
		})
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		if n == 0 {
			h.writeError(w, http.StatusConflict, "you can't delete your only passkey: it is your only way to sign in")
			return
		}
	} else if err := h.queries.DeleteWebauthnCredential(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
