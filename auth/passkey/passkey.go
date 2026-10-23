// Package passkey wires up the go-webauthn server library used for
// passkey (WebAuthn) registration and login, and adapts our sqlc-generated
// User/WebauthnCredential rows to the go-webauthn library's interfaces.
package passkey

import (
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
)

// New builds a *webauthn.WebAuthn configured for rpID (typically the
// console's bare domain, e.g. "app.example.com") and rpOrigin (the full
// origin the browser sees, e.g. "https://app.example.com").
func New(rpID, rpDisplayName, rpOrigin string) (_ *webauthn.WebAuthn, err error) {
	defer derrors.Wrap(&err, "passkey.New(%q, %q, %q)", rpID, rpDisplayName, rpOrigin)
	return webauthn.New(&webauthn.Config{
		RPDisplayName: rpDisplayName,
		RPID:          rpID,
		RPOrigins:     []string{rpOrigin},
	})
}

// User adapts an ownscale user plus their stored credentials to
// go-webauthn's webauthn.User interface.
type User struct {
	Row         sqlcgen.User
	Credentials []sqlcgen.WebauthnCredential
}

// WebAuthnID returns the user's ownscale id (an xid string) as the opaque
// WebAuthn user handle. xids are already unique/stable, so no separate
// handle needs to be minted or stored.
func (u User) WebAuthnID() []byte { return []byte(u.Row.ID) }

// WebAuthnName returns the user's name, shown by browser passkey UIs,
// falling back to their email when no name is set.
func (u User) WebAuthnName() string {
	if u.Row.Name != "" {
		return u.Row.Name
	}
	return u.Row.Email
}

// WebAuthnDisplayName returns the user's display name, falling back to
// their email when no name is set.
func (u User) WebAuthnDisplayName() string {
	if u.Row.Name != "" {
		return u.Row.Name
	}
	return u.Row.Email
}

// WebAuthnCredentials returns the user's stored credentials converted to
// go-webauthn's Credential type.
func (u User) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(u.Credentials))
	for _, c := range u.Credentials {
		out = append(out, RowToCredential(c))
	}
	return out
}

// RowToCredential converts a stored webauthn_credentials row to the
// go-webauthn library's in-memory Credential representation.
func RowToCredential(row sqlcgen.WebauthnCredential) webauthn.Credential {
	var transports []protocol.AuthenticatorTransport
	for t := range strings.SplitSeq(row.Transports, ",") {
		if t == "" {
			continue
		}
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}
	return webauthn.Credential{
		ID:              row.CredentialID,
		PublicKey:       row.PublicKey,
		AttestationType: row.AttestationType,
		Transport:       transports,
		Flags: webauthn.CredentialFlags{
			BackupEligible: row.BackupEligible != 0,
			BackupState:    row.BackupState != 0,
		},
		Authenticator: webauthn.Authenticator{
			SignCount: uint32(row.SignCount),
		},
	}
}

// CredentialParams builds the sqlc params to persist a freshly-registered
// go-webauthn Credential as a new webauthn_credentials row.
func CredentialParams(id, userID, name string, cred webauthn.Credential) sqlcgen.CreateWebauthnCredentialParams {
	transports := make([]string, 0, len(cred.Transport))
	for _, t := range cred.Transport {
		transports = append(transports, string(t))
	}
	backupEligible, backupState := int64(0), int64(0)
	if cred.Flags.BackupEligible {
		backupEligible = 1
	}
	if cred.Flags.BackupState {
		backupState = 1
	}
	return sqlcgen.CreateWebauthnCredentialParams{
		ID:              id,
		UserID:          userID,
		CredentialID:    cred.ID,
		PublicKey:       cred.PublicKey,
		AttestationType: cred.AttestationType,
		Transports:      strings.Join(transports, ","),
		SignCount:       int64(cred.Authenticator.SignCount),
		BackupEligible:  backupEligible,
		BackupState:     backupState,
		Name:            name,
	}
}
