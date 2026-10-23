package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	vwa "github.com/descope/virtualwebauthn"

	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// testRelyingParty mirrors the WebAuthn RP config handlers.New derives from
// baseDomain (see handlers.go): "console.example.com"/"https://console.example.com"
// when newTestHandlersWithBaseDomain(t, testBaseDomain) is used, or the
// "localhost" fallback when newTestHandlers(t) (baseDomain "") is used.
func testRelyingParty(baseDomain string) vwa.RelyingParty {
	if baseDomain == "" {
		return vwa.RelyingParty{Name: "Ownscale", ID: "localhost", Origin: "http://localhost"}
	}
	consoleDomain := "console." + baseDomain
	return vwa.RelyingParty{Name: "Ownscale", ID: consoleDomain, Origin: "https://" + consoleDomain}
}

// runCeremony drives a begin handler followed by its matching finish
// handler through a real (in-process) WebAuthn ceremony using a virtual
// authenticator, propagating the ceremony cookie the same way a browser
// would. build turns the begin response body into the string the browser
// would hand to navigator.credentials.{create,get}: an attestation
// response for registration, an assertion response for login.
func runCeremony(t *testing.T, beginFn, finishFn http.HandlerFunc, beginReq *http.Request, build func(beginBody string) string) *httptest.ResponseRecorder {
	t.Helper()

	beginRec := httptest.NewRecorder()
	beginFn(beginRec, beginReq)
	if beginRec.Code != http.StatusOK {
		t.Fatalf("begin status = %d, want 200: %s", beginRec.Code, beginRec.Body)
	}

	responseBody := build(beginRec.Body.String())

	finishReq := httptest.NewRequest(http.MethodPost, "/finish", strings.NewReader(responseBody))
	for _, c := range beginRec.Result().Cookies() {
		finishReq.AddCookie(c)
	}
	finishReq = finishReq.WithContext(beginReq.Context())

	finishRec := httptest.NewRecorder()
	finishFn(finishRec, finishReq)
	return finishRec
}

func registerBuilder(rp vwa.RelyingParty, authenticator vwa.Authenticator, cred vwa.Credential) func(string) string {
	return func(beginBody string) string {
		opts, err := vwa.ParseAttestationOptions(beginBody)
		if err != nil {
			panic(err)
		}
		return vwa.CreateAttestationResponse(rp, authenticator, cred, *opts)
	}
}

func loginBuilder(rp vwa.RelyingParty, authenticator vwa.Authenticator, cred vwa.Credential) func(string) string {
	return func(beginBody string) string {
		opts, err := vwa.ParseAssertionOptions(beginBody)
		if err != nil {
			panic(err)
		}
		return vwa.CreateAssertionResponse(rp, authenticator, cred, *opts)
	}
}

// TestPasskeyRegisterSelfServiceProvisionsOrganization exercises the full
// self-registration flow (no invite): a brand-new visitor registers a
// passkey directly, and finishes up auto-provisioned into a fresh
// organization as owner, exactly like a first-time OAuth login.
func TestPasskeyRegisterSelfServiceProvisionsOrganization(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlersWithBaseDomain(t, testBaseDomain)

	rp := testRelyingParty(testBaseDomain)
	authenticator := vwa.NewAuthenticator()
	cred := vwa.NewCredential(vwa.KeyTypeEC2)

	beginReq := httptest.NewRequest(http.MethodPost, "/api/passkey/register/begin",
		strings.NewReader(`{"username":"newuser"}`))
	finishRec := runCeremony(t, h.PasskeyRegisterBegin, h.PasskeyRegisterFinish, beginReq, registerBuilder(rp, authenticator, cred))

	if finishRec.Code != http.StatusSeeOther {
		t.Fatalf("finish status = %d, want %d: %s", finishRec.Code, http.StatusSeeOther, finishRec.Body)
	}
	if loc := finishRec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}

	// provider_user_id is a freshly minted xid we don't know ahead of time,
	// so look the user up by username instead.
	var found sqlcgen.User
	all := listAllUsers(t, queries)
	for _, u := range all {
		if u.Name == "newuser" {
			found = u
		}
	}
	if found.ID == "" {
		t.Fatalf("expected a user named newuser to have been created")
	}
	if found.Provider != passkeyProvider {
		t.Errorf("Provider = %q, want %q", found.Provider, passkeyProvider)
	}
	if found.Email != "newuser@passkey" {
		t.Errorf("Email = %q, want newuser@passkey", found.Email)
	}
	if found.OrganizationID == "" {
		t.Error("expected OrganizationID to be set (auto-provisioned)")
	}
	if found.Role != RoleOwner {
		t.Errorf("Role = %q, want %q", found.Role, RoleOwner)
	}

	creds, err := queries.ListWebauthnCredentialsByUser(t.Context(), found.ID)
	if err != nil {
		t.Fatalf("ListWebauthnCredentialsByUser: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("len(creds) = %d, want 1", len(creds))
	}
}

// TestPasskeyRegisterFinishAssignsInvitedOrganization mirrors
// TestAuthCallbackAssignsInvitedOrganization (auth_test.go) for the
// passkey registration path: a visitor registering via
// /login?next=/invite/{token} is created directly with the invite's
// organization and role, and the invite is marked accepted.
func TestPasskeyRegisterFinishAssignsInvitedOrganization(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlersWithBaseDomain(t, testBaseDomain)

	owner := insertUser(t, queries)
	org, err := queries.GetOrganization(t.Context(), owner.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	inv, err := queries.CreateInvite(t.Context(), sqlcgen.CreateInviteParams{
		ID:             "invite-passkey-1",
		OrganizationID: org.ID,
		Role:           RoleAdmin,
		Token:          "testtoken",
		CreatedBy:      owner.ID,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	rp := testRelyingParty(testBaseDomain)
	authenticator := vwa.NewAuthenticator()
	cred := vwa.NewCredential(vwa.KeyTypeEC2)

	beginReq := httptest.NewRequest(http.MethodPost, "/api/passkey/register/begin",
		strings.NewReader(`{"username":"inviteeuser","next":"/invite/testtoken"}`))
	finishRec := runCeremony(t, h.PasskeyRegisterBegin, h.PasskeyRegisterFinish, beginReq, registerBuilder(rp, authenticator, cred))

	if finishRec.Code != http.StatusSeeOther {
		t.Fatalf("finish status = %d, want %d: %s", finishRec.Code, http.StatusSeeOther, finishRec.Body)
	}
	if loc := finishRec.Header().Get("Location"); loc != "/invite/testtoken" {
		t.Errorf("Location = %q, want %q", loc, "/invite/testtoken")
	}

	var found sqlcgen.User
	for _, u := range listAllUsers(t, queries) {
		if u.Name == "inviteeuser" {
			found = u
		}
	}
	if found.ID == "" {
		t.Fatalf("expected a user named inviteeuser to have been created")
	}
	if found.OrganizationID != org.ID {
		t.Errorf("OrganizationID = %q, want the invited organization %q", found.OrganizationID, org.ID)
	}
	if found.Role != RoleAdmin {
		t.Errorf("Role = %q, want the invite's role %q", found.Role, RoleAdmin)
	}

	acceptedInv, err := queries.GetInvite(t.Context(), inv.ID)
	if err != nil {
		t.Fatalf("GetInvite: %v", err)
	}
	if !acceptedInv.AcceptedAt.Valid {
		t.Error("invite AcceptedAt not set, want it marked accepted")
	}
}

// TestPasskeyRegisterBeginRejectsInvalidUsername verifies the username
// validation PasskeyRegisterBegin performs itself.
func TestPasskeyRegisterBeginRejectsInvalidUsername(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing", `{}`},
		{"empty", `{"username":""}`},
		{"too short", `{"username":"a"}`},
		{"starts with digit", `{"username":"1alice"}`},
		{"bad char", `{"username":"al ice"}`},
		{"has at", `{"username":"alice@x"}`},
		{"reserved", `{"username":"devbox"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestHandlers(t)
			req := httptest.NewRequest(http.MethodPost, "/api/passkey/register/begin", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.PasskeyRegisterBegin(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

// TestPasskeyRegisterFinishRejectsExpiredCeremony verifies that finishing
// without a valid ceremony cookie (e.g. it expired, or was never set)
// fails cleanly instead of panicking.
func TestPasskeyRegisterFinishRejectsExpiredCeremony(t *testing.T) {
	h, _ := newTestHandlers(t)
	req := httptest.NewRequest(http.MethodPost, "/api/passkey/register/finish", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.PasskeyRegisterFinish(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

// TestPasskeyAddListLoginAndDelete exercises the Settings management flow
// end to end for an already-authenticated (OAuth-created) user: adding a
// passkey, listing it, logging in with it via the discoverable/usernameless
// flow, and deleting it -- plus verifies another user can't delete it.
func TestPasskeyAddListLoginAndDelete(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)
	owner := insertUser(t, queries)
	other := insertUser(t, queries)

	rp := testRelyingParty("")
	authenticator := vwa.NewAuthenticator()
	authenticator.Options.UserHandle = []byte(owner.ID)
	cred := vwa.NewCredential(vwa.KeyTypeEC2)

	addBeginReq := withTestUser(httptest.NewRequest(http.MethodPost, "/api/passkeys/register/begin",
		strings.NewReader(`{"name":"My Laptop"}`)), owner)
	addFinishRec := runCeremony(t, h.PasskeyAddBegin, h.PasskeyAddFinish, addBeginReq, registerBuilder(rp, authenticator, cred))
	if addFinishRec.Code != http.StatusCreated {
		t.Fatalf("add finish status = %d, want 201: %s", addFinishRec.Code, addFinishRec.Body)
	}
	var created struct {
		Passkey passkeyCredentialSummary `json:"passkey"`
	}
	if err := json.NewDecoder(addFinishRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Passkey.Name != "My Laptop" {
		t.Errorf("Name = %q, want %q", created.Passkey.Name, "My Laptop")
	}
	authenticator.AddCredential(cred)

	// List: the owner sees exactly the one passkey they just added.
	listReq := withTestUser(httptest.NewRequest(http.MethodGet, "/api/passkeys", nil), owner)
	listRec := httptest.NewRecorder()
	h.ListPasskeys(listRec, listReq)
	var listed struct {
		Passkeys []passkeyCredentialSummary `json:"passkeys"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Passkeys) != 1 || listed.Passkeys[0].ID != created.Passkey.ID {
		t.Fatalf("listed = %+v, want exactly the created passkey", listed.Passkeys)
	}

	// Login: the same passkey, used through the discoverable/usernameless
	// login endpoints (no email typed), authenticates as owner.
	loginBeginReq := httptest.NewRequest(http.MethodPost, "/api/passkey/login/begin", strings.NewReader(`{}`))
	loginFinishRec := runCeremony(t, h.PasskeyLoginBegin, h.PasskeyLoginFinish, loginBeginReq, loginBuilder(rp, authenticator, cred))
	if loginFinishRec.Code != http.StatusSeeOther {
		t.Fatalf("login finish status = %d, want %d: %s", loginFinishRec.Code, http.StatusSeeOther, loginFinishRec.Body)
	}

	// Delete: another user cannot delete owner's passkey.
	delReq := withTestUser(httptest.NewRequest(http.MethodDelete, "/api/passkeys/"+created.Passkey.ID, nil), other)
	delReq.SetPathValue("id", created.Passkey.ID)
	delRec := httptest.NewRecorder()
	h.DeletePasskey(delRec, delReq)
	if delRec.Code != http.StatusNotFound {
		t.Fatalf("other user's delete status = %d, want 404: %s", delRec.Code, delRec.Body)
	}

	// Delete: the owner can delete their own passkey.
	ownerDelReq := withTestUser(httptest.NewRequest(http.MethodDelete, "/api/passkeys/"+created.Passkey.ID, nil), owner)
	ownerDelReq.SetPathValue("id", created.Passkey.ID)
	ownerDelRec := httptest.NewRecorder()
	h.DeletePasskey(ownerDelRec, ownerDelReq)
	if ownerDelRec.Code != http.StatusOK {
		t.Fatalf("owner delete status = %d, want 200: %s", ownerDelRec.Code, ownerDelRec.Body)
	}

	remaining, err := queries.ListWebauthnCredentialsByUser(t.Context(), owner.ID)
	if err != nil {
		t.Fatalf("ListWebauthnCredentialsByUser: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("len(remaining) = %d, want 0 after delete", len(remaining))
	}
}

func TestPasskeyAddFinishRejectsMismatchedCeremonyUser(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)
	owner := insertUser(t, queries)
	other := insertUser(t, queries)

	beginReq := withTestUser(httptest.NewRequest(http.MethodPost, "/api/passkeys/register/begin", strings.NewReader(`{}`)), owner)
	beginRec := httptest.NewRecorder()
	h.PasskeyAddBegin(beginRec, beginReq)
	if beginRec.Code != http.StatusOK {
		t.Fatalf("begin status = %d, want 200: %s", beginRec.Code, beginRec.Body)
	}

	// other finishes the ceremony owner began, carrying owner's ceremony
	// cookie but authenticated as a different user.
	finishReq := withTestUser(httptest.NewRequest(http.MethodPost, "/api/passkeys/register/finish", strings.NewReader(`{}`)), other)
	for _, c := range beginRec.Result().Cookies() {
		finishReq.AddCookie(c)
	}
	finishRec := httptest.NewRecorder()
	h.PasskeyAddFinish(finishRec, finishReq)
	if finishRec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", finishRec.Code, finishRec.Body)
	}
}

func TestPasskeyAddFinishRejectsInvalidAttestation(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)
	owner := insertUser(t, queries)

	beginReq := withTestUser(httptest.NewRequest(http.MethodPost, "/api/passkeys/register/begin", strings.NewReader(`{}`)), owner)
	beginRec := httptest.NewRecorder()
	h.PasskeyAddBegin(beginRec, beginReq)
	if beginRec.Code != http.StatusOK {
		t.Fatalf("begin status = %d, want 200: %s", beginRec.Code, beginRec.Body)
	}

	// A well-formed JSON body that is nonetheless not a valid attestation
	// response makes FinishRegistration fail verification.
	finishReq := withTestUser(httptest.NewRequest(http.MethodPost, "/api/passkeys/register/finish", strings.NewReader(`{"id":"bogus"}`)), owner)
	for _, c := range beginRec.Result().Cookies() {
		finishReq.AddCookie(c)
	}
	finishRec := httptest.NewRecorder()
	h.PasskeyAddFinish(finishRec, finishReq)
	if finishRec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", finishRec.Code, finishRec.Body)
	}
}

func TestDeletePasskeyNotFound(t *testing.T) {
	h, queries := newTestHandlers(t)
	owner := insertUser(t, queries)

	req := withTestUser(httptest.NewRequest(http.MethodDelete, "/api/passkeys/nonexistent", nil), owner)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	h.DeletePasskey(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestDeletePasskeyUnauthenticated(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/passkeys/x", nil)
	req.SetPathValue("id", "x")
	rec := httptest.NewRecorder()
	h.DeletePasskey(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body)
	}
}

// listAllUsers is a small test helper: there's no "get user by email"
// query (email isn't unique across providers), so tests that need to find
// a just-created user by email scan every row.
func listAllUsers(t *testing.T, queries interface {
	ListUsersWithOrganization(ctx context.Context) ([]sqlcgen.ListUsersWithOrganizationRow, error)
}) []sqlcgen.User {
	t.Helper()
	rows, err := queries.ListUsersWithOrganization(t.Context())
	if err != nil {
		t.Fatalf("ListUsersWithOrganization: %v", err)
	}
	out := make([]sqlcgen.User, 0, len(rows))
	for _, r := range rows {
		out = append(out, sqlcgen.User{
			ID:              r.ID,
			OrganizationID:  r.OrganizationID,
			Provider:        r.Provider,
			ProviderUserID:  r.ProviderUserID,
			Email:           r.Email,
			Name:            r.Name,
			AvatarUrl:       r.AvatarUrl,
			CreatedAt:       r.CreatedAt,
			UpdatedAt:       r.UpdatedAt,
			EmailVerified:   r.EmailVerified,
			LastLoginAt:     r.LastLoginAt,
			Role:            r.Role,
			HeadscaleUserID: r.HeadscaleUserID,
		})
	}
	return out
}

// passkeySignup drives a full passkey registration ceremony for username
// into the org the invite token points at, returning the finish recorder.
func passkeySignup(t *testing.T, h *Handlers, username, token string) *httptest.ResponseRecorder {
	t.Helper()
	rp := testRelyingParty(testBaseDomain)
	body := `{"username":"` + username + `","next":"/invite/` + token + `"}`
	beginReq := httptest.NewRequest(http.MethodPost, "/api/passkey/register/begin", strings.NewReader(body))
	return runCeremony(t, h.PasskeyRegisterBegin, h.PasskeyRegisterFinish, beginReq,
		registerBuilder(rp, vwa.NewAuthenticator(), vwa.NewCredential(vwa.KeyTypeEC2)))
}

// inviteInto creates an invite with the given token into org, by creator.
func inviteInto(t *testing.T, queries *dbpkg.TxQueries, org, creator, token string) {
	t.Helper()
	if _, err := queries.CreateInvite(t.Context(), sqlcgen.CreateInviteParams{
		ID:             "invite-" + token,
		OrganizationID: org,
		Role:           RoleMember,
		Token:          token,
		CreatedBy:      creator,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateInvite(%q): %v", token, err)
	}
}

// TestPasskeyUsernameUniquePerOrg: a username can be taken only once within
// an organization, but the same username is free in a different one.
func TestPasskeyUsernameUniquePerOrg(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlersWithBaseDomain(t, testBaseDomain)

	ownerA := insertUser(t, queries)
	inviteInto(t, queries, ownerA.OrganizationID, ownerA.ID, "a1token")
	inviteInto(t, queries, ownerA.OrganizationID, ownerA.ID, "a2token")

	if rec := passkeySignup(t, h, "alice", "a1token"); rec.Code != http.StatusSeeOther {
		t.Fatalf("first signup status = %d, want 303: %s", rec.Code, rec.Body)
	}
	if rec := passkeySignup(t, h, "alice", "a2token"); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate signup status = %d, want 409: %s", rec.Code, rec.Body)
	}

	ownerB := insertUser(t, queries)
	inviteInto(t, queries, ownerB.OrganizationID, ownerB.ID, "b1token")
	if rec := passkeySignup(t, h, "alice", "b1token"); rec.Code != http.StatusSeeOther {
		t.Fatalf("same username in another org status = %d, want 303: %s", rec.Code, rec.Body)
	}
}

func TestValidatePasskeyUsername(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"alice", "alice", true},
		{"al", "al", true},
		{"Alice", "alice", true},
		{"  bob_1.2-3  ", "bob_1.2-3", true},
		{"a", "", false},
		{"", "", false},
		{"1alice", "", false},
		{"al ice", "", false},
		{"alice@x", "", false},
		{"devbox", "", false},
		{"user", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, msg := validatePasskeyUsername(tt.in)
			if (msg == "") != tt.wantOK {
				t.Fatalf("validatePasskeyUsername(%q) msg = %q, wantOK = %v", tt.in, msg, tt.wantOK)
			}
			if tt.wantOK && got != tt.want {
				t.Errorf("normalized = %q, want %q", got, tt.want)
			}
		})
	}
}
