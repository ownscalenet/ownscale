package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
)

// recordingRunner is a fake restoreRunner that records every call it
// receives (instead of shelling out to litestream for real) and, unless
// err is set, creates an empty file at localPath so callers checking
// os.Stat afterward see it as "restored".
type recordingRunner struct {
	calls []restoreCall
	err   error
}

type restoreCall struct {
	localPath  string
	replicaURL string
}

func (r *recordingRunner) run(_ context.Context, localPath, replicaURL string) error {
	r.calls = append(r.calls, restoreCall{localPath: localPath, replicaURL: replicaURL})
	if r.err != nil {
		return r.err
	}
	return os.WriteFile(localPath, []byte("restored"), 0o644)
}

func TestRestoreIfMissing_NoReplicaConfigured(t *testing.T) {
	runner := &recordingRunner{}
	dataDir := t.TempDir()

	if err := restoreIfMissing(t.Context(), &bytes.Buffer{}, runner.run, "", dataDir, "ownscale.sqlite"); err != nil {
		t.Fatalf("restoreIfMissing: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("expected no restore calls when replica URL is unset, got %d", len(runner.calls))
	}
	if _, err := os.Stat(filepath.Join(dataDir, "ownscale.sqlite")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no file to be created, stat err = %v", err)
	}
}

func TestRestoreIfMissing_AlreadyExists(t *testing.T) {
	runner := &recordingRunner{}
	dataDir := t.TempDir()
	existing := filepath.Join(dataDir, "ownscale.sqlite")
	if err := os.WriteFile(existing, []byte("already here"), 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}

	if err := restoreIfMissing(t.Context(), &bytes.Buffer{}, runner.run, "abs://acct@container/path", dataDir, "ownscale.sqlite"); err != nil {
		t.Fatalf("restoreIfMissing: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("expected no restore calls when file already exists, got %d", len(runner.calls))
	}
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read existing file: %v", err)
	}
	if string(got) != "already here" {
		t.Errorf("existing file was overwritten, got %q", got)
	}
}

func TestRestoreIfMissing_Missing(t *testing.T) {
	runner := &recordingRunner{}
	dataDir := t.TempDir()

	err := restoreIfMissing(t.Context(), &bytes.Buffer{}, runner.run, "abs://acct@container/path", dataDir, filepath.Join("headscale", "acme", "db.sqlite"))
	if err != nil {
		t.Fatalf("restoreIfMissing: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected exactly 1 restore call, got %d", len(runner.calls))
	}
	wantLocal := filepath.Join(dataDir, "headscale", "acme", "db.sqlite")
	wantURL := "abs://acct@container/path/headscale/acme/db.sqlite"
	if runner.calls[0].localPath != wantLocal {
		t.Errorf("localPath = %q, want %q", runner.calls[0].localPath, wantLocal)
	}
	if runner.calls[0].replicaURL != wantURL {
		t.Errorf("replicaURL = %q, want %q", runner.calls[0].replicaURL, wantURL)
	}
	if _, err := os.Stat(wantLocal); err != nil {
		t.Errorf("expected file to now exist: %v", err)
	}
}

func TestRestoreIfMissing_RunnerError(t *testing.T) {
	wantErr := errors.New("boom")
	runner := &recordingRunner{err: wantErr}
	dataDir := t.TempDir()

	err := restoreIfMissing(t.Context(), &bytes.Buffer{}, runner.run, "abs://acct@container/path", dataDir, "ownscale.sqlite")
	if !errors.Is(err, wantErr) {
		t.Errorf("expected error to wrap %v, got %v", wantErr, err)
	}
}

func TestRestoreOrganizations(t *testing.T) {
	_, queries := dbtest.New(t)
	dbtest.CreateOrg(t, queries.Queries, "acme")
	dbtest.CreateOrg(t, queries.Queries, "globex")

	dataDir := t.TempDir()
	// globex already has both files locally; acme has neither.
	globexHS := filepath.Join(dataDir, "headscale", "globex", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(globexHS), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(globexHS, []byte("existing"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	runner := &recordingRunner{}
	if err := restoreOrganizations(t.Context(), &bytes.Buffer{}, runner.run, "abs://acct@container/path", dataDir, queries); err != nil {
		t.Fatalf("restoreOrganizations: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 restore calls (only for acme's missing files), got %d: %+v", len(runner.calls), runner.calls)
	}
	wantURLs := map[string]bool{
		"abs://acct@container/path/headscale/acme/db.sqlite": false,
	}
	for _, call := range runner.calls {
		if _, ok := wantURLs[call.replicaURL]; !ok {
			t.Errorf("unexpected restore call for %q", call.replicaURL)
			continue
		}
		wantURLs[call.replicaURL] = true
	}
	for url, called := range wantURLs {
		if !called {
			t.Errorf("expected a restore call for %q, got none", url)
		}
	}

	// globex's pre-existing files must not have been touched.
	got, err := os.ReadFile(globexHS)
	if err != nil {
		t.Fatalf("read %s: %v", globexHS, err)
	}
	if string(got) != "existing" {
		t.Errorf("%s was overwritten, got %q", globexHS, got)
	}
}

func TestRestoreOrganizations_NoOrganizations(t *testing.T) {
	_, queries := dbtest.New(t)
	runner := &recordingRunner{}
	dataDir := t.TempDir()

	if err := restoreOrganizations(t.Context(), &bytes.Buffer{}, runner.run, "abs://acct@container/path", dataDir, queries); err != nil {
		t.Fatalf("restoreOrganizations: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("expected no restore calls with no organizations, got %d", len(runner.calls))
	}
}
