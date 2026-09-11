package passkey

import (
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

func TestNewBuildsWebAuthnFromRPConfig(t *testing.T) {
	w, err := New("example.com", "Ownscale", "https://example.com")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.Config.RPID != "example.com" {
		t.Errorf("RPID = %q, want %q", w.Config.RPID, "example.com")
	}
	if len(w.Config.RPOrigins) != 1 || w.Config.RPOrigins[0] != "https://example.com" {
		t.Errorf("RPOrigins = %v, want [https://example.com]", w.Config.RPOrigins)
	}
}

func TestUserWebAuthnFields(t *testing.T) {
	u := User{Row: sqlcgen.User{ID: "u1", Email: "a@example.com"}}
	if string(u.WebAuthnID()) != "u1" {
		t.Errorf("WebAuthnID = %q, want %q", u.WebAuthnID(), "u1")
	}
	if u.WebAuthnName() != "a@example.com" {
		t.Errorf("WebAuthnName = %q, want %q", u.WebAuthnName(), "a@example.com")
	}
	// No Name set: display name falls back to email.
	if u.WebAuthnDisplayName() != "a@example.com" {
		t.Errorf("WebAuthnDisplayName = %q, want fallback to email", u.WebAuthnDisplayName())
	}

	u.Row.Name = "Alice"
	if u.WebAuthnDisplayName() != "Alice" {
		t.Errorf("WebAuthnDisplayName = %q, want %q", u.WebAuthnDisplayName(), "Alice")
	}
}

func TestCredentialRowRoundTrip(t *testing.T) {
	cred := webauthn.Credential{
		ID:              []byte{1, 2, 3, 4},
		PublicKey:       []byte{5, 6, 7, 8},
		AttestationType: "none",
		Transport:       []protocol.AuthenticatorTransport{protocol.Internal, protocol.Hybrid},
		Flags: webauthn.CredentialFlags{
			BackupEligible: true,
			BackupState:    true,
		},
		Authenticator: webauthn.Authenticator{SignCount: 7},
	}

	params := CredentialParams("cred-id", "user-id", "My Passkey", cred)
	if params.UserID != "user-id" || params.Name != "My Passkey" {
		t.Fatalf("unexpected params: %+v", params)
	}
	if params.Transports != "internal,hybrid" {
		t.Errorf("Transports = %q, want %q", params.Transports, "internal,hybrid")
	}
	if params.BackupEligible != 1 || params.BackupState != 1 {
		t.Errorf("BackupEligible/BackupState = %d/%d, want 1/1", params.BackupEligible, params.BackupState)
	}
	if params.SignCount != 7 {
		t.Errorf("SignCount = %d, want 7", params.SignCount)
	}

	row := sqlcgen.WebauthnCredential{
		CredentialID:    params.CredentialID,
		PublicKey:       params.PublicKey,
		AttestationType: params.AttestationType,
		Transports:      params.Transports,
		SignCount:       params.SignCount,
		BackupEligible:  params.BackupEligible,
		BackupState:     params.BackupState,
	}
	back := RowToCredential(row)
	if string(back.ID) != string(cred.ID) || string(back.PublicKey) != string(cred.PublicKey) {
		t.Fatalf("round-tripped credential = %+v, want ID/PublicKey to match original", back)
	}
	if back.Authenticator.SignCount != 7 {
		t.Errorf("SignCount = %d, want 7", back.Authenticator.SignCount)
	}
	if len(back.Transport) != 2 {
		t.Fatalf("Transport = %v, want 2 entries", back.Transport)
	}
	if !back.Flags.BackupEligible || !back.Flags.BackupState {
		t.Errorf("Flags = %+v, want both backup flags set", back.Flags)
	}
}

func TestRowToCredentialHandlesEmptyTransports(t *testing.T) {
	row := sqlcgen.WebauthnCredential{Transports: ""}
	cred := RowToCredential(row)
	if len(cred.Transport) != 0 {
		t.Errorf("Transport = %v, want empty", cred.Transport)
	}
}

func TestUserWebAuthnCredentials(t *testing.T) {
	u := User{
		Row: sqlcgen.User{ID: "u1"},
		Credentials: []sqlcgen.WebauthnCredential{
			{CredentialID: []byte{1}, PublicKey: []byte{2}},
			{CredentialID: []byte{3}, PublicKey: []byte{4}},
		},
	}
	creds := u.WebAuthnCredentials()
	if len(creds) != 2 {
		t.Fatalf("len(creds) = %d, want 2", len(creds))
	}
}
