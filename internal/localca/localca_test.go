package localca

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrGenerateCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "local-ca")

	generated, err := LoadOrGenerate(sub)
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}
	if !generated {
		t.Errorf("LoadOrGenerate: generated = false, want true for a fresh CA")
	}

	certPath := filepath.Join(sub, RootCertFileName)
	keyPath := filepath.Join(sub, RootKeyFileName)

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}

	cert, err := parseCertAndKey(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parseCertAndKey: %v", err)
	}
	if !cert.IsCA {
		t.Errorf("generated certificate is not a CA")
	}
}

func TestLoadOrGenerateIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	generated, err := LoadOrGenerate(dir)
	if err != nil {
		t.Fatalf("first LoadOrGenerate: %v", err)
	}
	if !generated {
		t.Errorf("first LoadOrGenerate: generated = false, want true")
	}
	firstCert, err := os.ReadFile(filepath.Join(dir, RootCertFileName))
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}

	generated, err = LoadOrGenerate(dir)
	if err != nil {
		t.Fatalf("second LoadOrGenerate: %v", err)
	}
	if generated {
		t.Errorf("second LoadOrGenerate: generated = true, want false for a reused CA")
	}
	secondCert, err := os.ReadFile(filepath.Join(dir, RootCertFileName))
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}

	if string(firstCert) != string(secondCert) {
		t.Errorf("second LoadOrGenerate call regenerated the CA instead of reusing it")
	}
}

func TestLoadOrGenerateRejectsCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RootCertFileName), []byte("not a cert"), 0o644); err != nil {
		t.Fatalf("write bogus cert: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, RootKeyFileName), []byte("not a key"), 0o600); err != nil {
		t.Fatalf("write bogus key: %v", err)
	}

	if _, err := LoadOrGenerate(dir); err == nil {
		t.Errorf("expected an error for a corrupt existing CA, got nil")
	}
}

func TestLoadOrGenerateRejectsCertWithoutKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrGenerate(dir); err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, RootKeyFileName)); err != nil {
		t.Fatalf("remove key: %v", err)
	}

	if _, err := LoadOrGenerate(dir); err == nil {
		t.Errorf("expected an error for a cert present without its key, got nil")
	}
}

func TestResetCaddyStateRemovesCachedIntermediateAndCerts(t *testing.T) {
	dir := t.TempDir()

	pkiDir := filepath.Join(dir, "caddy", "pki", "authorities", "local")
	certsDir := filepath.Join(dir, "caddy", "certificates", "local", "dex.ownscale.test")
	if err := os.MkdirAll(pkiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll pki: %v", err)
	}
	if err := os.MkdirAll(certsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll certs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkiDir, "intermediate.crt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale intermediate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(certsDir, "dex.ownscale.test.crt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale cert: %v", err)
	}

	if err := ResetCaddyState(dir); err != nil {
		t.Fatalf("ResetCaddyState: %v", err)
	}

	if _, err := os.Stat(pkiDir); !os.IsNotExist(err) {
		t.Errorf("stale pki dir still exists (err = %v)", err)
	}
	if _, err := os.Stat(certsDir); !os.IsNotExist(err) {
		t.Errorf("stale certs dir still exists (err = %v)", err)
	}
}

func TestResetCaddyStateMissingDirIsNoop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if err := ResetCaddyState(dir); err != nil {
		t.Errorf("ResetCaddyState on missing dir: %v", err)
	}
}
