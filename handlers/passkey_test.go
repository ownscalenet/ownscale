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
	provisionerCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.StartProvisioner(provisionerCtx)

	rp := testRelyingParty(testBaseDomain)
	authenticator := vwa.NewAuthenticator()
	cred := vwa.NewCredential(vwa.KeyTypeEC2)

	beginReq := httptest.NewRequest(http.MethodPost, "/api/passkey/register/begin",
		strings.NewReader(`{"email":"new-passkey-user@example.com"}`))
	finishRec := runCeremony(t, h.PasskeyRegisterBegin, h.PasskeyRegisterFinish, beginReq, registerBuilder(rp, authenticator, cred))

	if finishRec.Code != http.StatusSeeOther {
		t.Fatalf("finish status = %d, want %d: %s", finishRec.Code, http.StatusSeeOther, finishRec.Body)
	}
	if loc := finishRec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}

	// provider_user_id is a freshly minted xid we don't know ahead of time,
	// so look the user up by email instead.
	var found sqlcgen.User
	all := listAllUsers(t, queries)
	for _, u := range all {
		if u.Email == "new-passkey-user@example.com" {
			found = u
		}
	}
	if found.ID == "" {
		t.Fatalf("expected a user with email new-passkey-user@example.com to have been created")
	}
	if found.Provider != passkeyProvider {
		t.Errorf("Provider = %q, want %q", found.Provider, passkeyProvider)
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
	provisionerCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.StartProvisioner(provisionerCtx)

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
		strings.NewReader(`{"email":"invitee-passkey@example.com","next":"/invite/testtoken"}`))
	finishRec := runCeremony(t, h.PasskeyRegisterBegin, h.PasskeyRegisterFinish, beginReq, registerBuilder(rp, authenticator, cred))

	if finishRec.Code != http.StatusSeeOther {
		t.Fatalf("finish status = %d, want %d: %s", finishRec.Code, http.StatusSeeOther, finishRec.Body)
	}
	if loc := finishRec.Header().Get("Location"); loc != "/invite/testtoken" {
		t.Errorf("Location = %q, want %q", loc, "/invite/testtoken")
	}

	var found sqlcgen.User
	for _, u := range listAllUsers(t, queries) {
		if u.Email == "invitee-passkey@example.com" {
			found = u
		}
	}
	if found.ID == "" {
		t.Fatalf("expected a user with email invitee-passkey@example.com to have been created")
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

// TestPasskeyRegisterBeginRejectsMissingEmail verifies the (only)
// validation PasskeyRegisterBegin performs itself.
func TestPasskeyRegisterBeginRejectsMissingEmail(t *testing.T) {
	h, _ := newTestHandlers(t)
	req := httptest.NewRequest(http.MethodPost, "/api/passkey/register/begin", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.PasskeyRegisterBegin(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
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
