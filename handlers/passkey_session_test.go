package handlers

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestPasskeyCeremonyStoreSaveAndTake(t *testing.T) {
	s := newPasskeyCeremonyStore()
	tok, err := s.save(passkeyCeremony{Email: "a@example.com"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := s.take(tok)
	if !ok {
		t.Fatal("take: want ok=true")
	}
	if got.Email != "a@example.com" {
		t.Errorf("Email = %q, want %q", got.Email, "a@example.com")
	}

	// A ceremony can only be completed once.
	if _, ok := s.take(tok); ok {
		t.Error("take again: want ok=false, ceremonies are single-use")
	}
}

func TestPasskeyCeremonyStoreUnknownToken(t *testing.T) {
	s := newPasskeyCeremonyStore()
	if _, ok := s.take("does-not-exist"); ok {
		t.Error("take unknown token: want ok=false")
	}
}

// TestPasskeyCeremonyStoreFull verifies a full store makes room by dropping
// expired ceremonies, and refuses new ones otherwise.
func TestPasskeyCeremonyStoreFull(t *testing.T) {
	tests := []struct {
		name    string
		age     time.Duration
		wantErr error
	}{
		{"all live", 0, errTooManyCeremonies},
		{"all expired", 2 * passkeyCeremonyTTL, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newPasskeyCeremonyStore()
			created := time.Now().Add(-tt.age)
			for i := range maxPasskeyCeremonies {
				s.byTok[strconv.Itoa(i)] = passkeyCeremony{CreatedAt: created}
			}
			_, err := s.save(passkeyCeremony{})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("save: err = %v, want %v", err, tt.wantErr)
			}
			if len(s.byTok) > maxPasskeyCeremonies {
				t.Errorf("store holds %d ceremonies, want at most %d", len(s.byTok), maxPasskeyCeremonies)
			}
		})
	}
}

func TestPasskeyCeremonyStoreExpiry(t *testing.T) {
	s := newPasskeyCeremonyStore()
	tok, err := s.save(passkeyCeremony{Email: "old@example.com"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	// Simulate the ceremony having been created well before the TTL.
	s.mu.Lock()
	c := s.byTok[tok]
	c.CreatedAt = time.Now().Add(-2 * passkeyCeremonyTTL)
	s.byTok[tok] = c
	s.mu.Unlock()

	if _, ok := s.take(tok); ok {
		t.Error("take expired token: want ok=false")
	}
}
