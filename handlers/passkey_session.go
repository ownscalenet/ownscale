package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

const passkeyCeremonyTTL = 5 * time.Minute
const passkeyCeremonyCookie = "__Host-ownscale_webauthn_session"

// passkeyCeremony holds everything a register/login "begin" step needs to
// pass along to its matching "finish" step. Email is set only for
// self-registration (anonymous, no user yet); UserID is set only when an
// already-authenticated caller is adding an extra passkey via Settings;
// Next preserves the post-login redirect target (e.g. an invite accept
// path) across the ceremony, the same way nextCookieName does for OAuth.
type passkeyCeremony struct {
	Session       *webauthn.SessionData
	Username      string
	Email         string
	PendingUserID string
	UserID        string
	Name          string
	Next          string
	CreatedAt     time.Time
}

// passkeyCeremonyStore is an in-memory, TTL-expiring store for in-flight
// WebAuthn ceremonies. It's process-local: if ownscale ever runs multiple
// replicas behind a load balancer without sticky sessions, this would need
// to move to the database or a shared cache.
type passkeyCeremonyStore struct {
	mu    sync.Mutex
	byTok map[string]passkeyCeremony
}

func newPasskeyCeremonyStore() *passkeyCeremonyStore {
	return &passkeyCeremonyStore{byTok: make(map[string]passkeyCeremony)}
}

// randomCeremonyToken returns a random URL-safe hex token used to key a
// ceremony in the store and as the passkeyCeremonyCookie's value.
func randomCeremonyToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// save stores c, evicts expired entries opportunistically, and returns the
// token the caller should set as the ceremony cookie's value.
func (s *passkeyCeremonyStore) save(c passkeyCeremony) (string, error) {
	tok, err := randomCeremonyToken()
	if err != nil {
		return "", err
	}
	c.CreatedAt = time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked()
	s.byTok[tok] = c
	return tok, nil
}

// take removes and returns the ceremony for tok, if present and not
// expired. A ceremony can only be completed once.
func (s *passkeyCeremonyStore) take(tok string) (passkeyCeremony, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byTok[tok]
	delete(s.byTok, tok)
	if !ok || time.Since(c.CreatedAt) > passkeyCeremonyTTL {
		return passkeyCeremony{}, false
	}
	return c, true
}

// evictExpiredLocked drops stale entries left behind by abandoned
// ceremonies. Callers must hold s.mu.
func (s *passkeyCeremonyStore) evictExpiredLocked() {
	now := time.Now()
	for tok, c := range s.byTok {
		if now.Sub(c.CreatedAt) > passkeyCeremonyTTL {
			delete(s.byTok, tok)
		}
	}
}
