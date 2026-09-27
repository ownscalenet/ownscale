package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
)

// fakeVerifier fakes both statusChecker and replicaHasBackup for a single
// database path, so tests don't need to shell out to the real litestream
// binary.
type fakeVerifier struct {
	// status maps localPath -> the status litestreamStatus would report.
	status map[string]string
	// backed maps replicaURL -> whether the replica has any backup content.
	backed map[string]bool

	statusErr error
	backedErr error

	statusCalls []string
	backedCalls []string
}

func (f *fakeVerifier) checkStatus(_ context.Context, localPath, _ string) (string, error) {
	f.statusCalls = append(f.statusCalls, localPath)
	if f.statusErr != nil {
		return "", f.statusErr
	}
	return f.status[localPath], nil
}

func (f *fakeVerifier) hasBackup(_ context.Context, replicaURL string) (bool, error) {
	f.backedCalls = append(f.backedCalls, replicaURL)
	if f.backedErr != nil {
		return false, f.backedErr
	}
	return f.backed[replicaURL], nil
}

func TestVerifyHealthy_NoReplicaConfigured(t *testing.T) {
	f := &fakeVerifier{}
	dataDir := t.TempDir()

	if err := verifyHealthy(t.Context(), &bytes.Buffer{}, f.checkStatus, f.hasBackup, "", dataDir, "ownscale.sqlite"); err != nil {
		t.Fatalf("verifyHealthy: %v", err)
	}
	if len(f.statusCalls) != 0 || len(f.backedCalls) != 0 {
		t.Errorf("expected no calls when replica URL is unset, got status=%d backed=%d", len(f.statusCalls), len(f.backedCalls))
	}
}

func TestVerifyHealthy_StatusOK(t *testing.T) {
	dataDir := t.TempDir()
	localPath := dataDir + "/ownscale.sqlite"
	f := &fakeVerifier{status: map[string]string{localPath: "ok"}}

	if err := verifyHealthy(t.Context(), &bytes.Buffer{}, f.checkStatus, f.hasBackup, "abs://acct@container/path", dataDir, "ownscale.sqlite"); err != nil {
		t.Fatalf("verifyHealthy: %v", err)
	}
	if len(f.backedCalls) != 0 {
		t.Errorf("expected no hasBackup call when status is ok, got %d", len(f.backedCalls))
	}
}

func TestVerifyHealthy_UnhealthyButNoBackupYet(t *testing.T) {
	dataDir := t.TempDir()
	localPath := dataDir + "/ownscale.sqlite"
	replicaURL := "abs://acct@container/path/ownscale.sqlite"
	f := &fakeVerifier{
		status: map[string]string{localPath: "no database"},
		backed: map[string]bool{replicaURL: false},
	}

	if err := verifyHealthy(t.Context(), &bytes.Buffer{}, f.checkStatus, f.hasBackup, "abs://acct@container/path", dataDir, "ownscale.sqlite"); err != nil {
		t.Fatalf("verifyHealthy: expected no error for a brand-new never-backed-up database, got %v", err)
	}
}

func TestVerifyHealthy_UnhealthyWithExistingBackup(t *testing.T) {
	dataDir := t.TempDir()
	localPath := dataDir + "/ownscale.sqlite"
	replicaURL := "abs://acct@container/path/ownscale.sqlite"
	f := &fakeVerifier{
		status: map[string]string{localPath: "no database"},
		backed: map[string]bool{replicaURL: true},
	}

	err := verifyHealthy(t.Context(), &bytes.Buffer{}, f.checkStatus, f.hasBackup, "abs://acct@container/path", dataDir, "ownscale.sqlite")
	if err == nil {
		t.Fatal("verifyHealthy: expected an error when unhealthy with an existing backup, got nil")
	}
}

func TestVerifyHealthy_StatusCheckError(t *testing.T) {
	wantErr := errors.New("boom")
	f := &fakeVerifier{statusErr: wantErr}
	dataDir := t.TempDir()

	err := verifyHealthy(t.Context(), &bytes.Buffer{}, f.checkStatus, f.hasBackup, "abs://acct@container/path", dataDir, "ownscale.sqlite")
	if !errors.Is(err, wantErr) {
		t.Errorf("expected error to wrap %v, got %v", wantErr, err)
	}
}

func TestVerifyOrganizationsHealthy(t *testing.T) {
	_, queries := dbtest.New(t)
	dbtest.CreateOrg(t, queries.Queries, "acme")
	dbtest.CreateOrg(t, queries.Queries, "globex")

	dataDir := t.TempDir()
	acmeHSLocal := dataDir + "/headscale/acme/db.sqlite"
	globexHSLocal := dataDir + "/headscale/globex/db.sqlite"

	f := &fakeVerifier{
		status: map[string]string{
			acmeHSLocal:   "ok",
			globexHSLocal: "no database", // unhealthy...
		},
		backed: map[string]bool{
			"abs://acct@container/path/headscale/globex/db.sqlite": true, // ...and has a real backup, so must fail
		},
	}

	err := verifyOrganizationsHealthy(t.Context(), &bytes.Buffer{}, f.checkStatus, f.hasBackup, "abs://acct@container/path", dataDir, queries)
	if err == nil {
		t.Fatal("expected an error for globex's unhealthy-with-backup headscale db, got nil")
	}
	if !strings.Contains(err.Error(), "globex") {
		t.Errorf("expected error to mention globex, got %v", err)
	}
	if strings.Contains(err.Error(), "acme") {
		t.Errorf("expected error to not mention acme (which is healthy), got %v", err)
	}
}

func TestVerifyOrganizationsHealthy_AllHealthy(t *testing.T) {
	_, queries := dbtest.New(t)
	dbtest.CreateOrg(t, queries.Queries, "acme")

	dataDir := t.TempDir()
	f := &fakeVerifier{
		status: map[string]string{
			dataDir + "/headscale/acme/db.sqlite": "ok",
		},
	}

	if err := verifyOrganizationsHealthy(t.Context(), &bytes.Buffer{}, f.checkStatus, f.hasBackup, "abs://acct@container/path", dataDir, queries); err != nil {
		t.Fatalf("verifyOrganizationsHealthy: %v", err)
	}
}
