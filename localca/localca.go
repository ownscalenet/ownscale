// Package localca generates and loads a self-signed root CA used only for
// local development, when BASE_DOMAIN isn't a real, publicly resolvable
// domain and Caddy therefore can't get publicly-trusted ACME certificates
// (see docker-compose.yml's CADDY_LOCAL_CERTS env var).
//
// Unlike Caddy's own built-in local CA (which it generates lazily, the
// first time it issues an internal certificate), this CA is generated
// ahead of time -- via the `osa gen-local-ca` subcommand, run once before
// Caddy or ownscale ever start (see docker-compose.yml's "local-ca-init"
// service) -- so its root certificate is a plain, already-existing file
// from the very start. That lets every container that needs to trust it
// (Caddy itself, which is configured to sign all its local certs from
// this CA -- see etc/caddy/Caddyfile's "pki" block -- plus ownscale,
// headscale, and headplane, which all just need to trust it) get there via
// a single, static, read-only bind-mount, with no Docker-API file copying
// or merging between containers at runtime.
package localca

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/ownscalenet/ownscale/derrors"
)

// RootCertFileName and RootKeyFileName are the fixed file names
// LoadOrGenerate reads/writes within a LocalCADir, and that Caddy's own
// "pki" config (etc/caddy/Caddyfile) and dockerctl's bind-mounts into
// organization containers both expect.
const (
	RootCertFileName = "root.crt"
	RootKeyFileName  = "root.key"
)

// validity is how long the generated root CA is valid for. Long-lived
// since it's local-dev-only and regenerating it would require every
// developer to re-trust a new CA; regenerate manually (delete the files
// under LocalCADir and rerun `osa gen-local-ca`) if it ever needs to
// change.
const validity = 10 * 365 * 24 * time.Hour

// LoadOrGenerate creates a self-signed root CA (RootCertFileName and
// RootKeyFileName) under dir if they don't already exist, and is a no-op
// (validating both files parse) if they do -- so it's safe to call every
// time (e.g. on every `osa gen-local-ca` invocation, including repeated
// ones across container restarts). It reports whether a new CA was
// generated (as opposed to an existing one being reused), so callers that
// also manage state derived from this CA elsewhere (see ResetCaddyState)
// know when that state has just been invalidated.
func LoadOrGenerate(dir string) (generated bool, err error) {
	defer derrors.Wrap(&err, "localca.LoadOrGenerate(%q)", dir)

	certPath := filepath.Join(dir, RootCertFileName)
	keyPath := filepath.Join(dir, RootKeyFileName)

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		if _, err := parseCertAndKey(certPEM, keyPEM); err != nil {
			return false, fmt.Errorf("existing CA at %s is invalid: %w", dir, err)
		}
		return false, nil
	case os.IsNotExist(certErr) && os.IsNotExist(keyErr):
		// Neither exists: generate below.
	case certErr != nil:
		return false, certErr
	default:
		return false, keyErr
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return false, fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return false, fmt.Errorf("generate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ownscale local development CA", Organization: []string{"ownscale (local dev)"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return false, fmt.Errorf("create certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	// Write the cert before the key: if we crash/are killed between the two
	// writes, LoadOrGenerate's "both must exist" check above will correctly
	// treat a cert-without-key as an error on the next run rather than
	// silently reusing a certificate whose key is gone.
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", certPath, err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return false, fmt.Errorf("write %s: %w", keyPath, err)
	}
	return true, nil
}

// When deleting data, a new CA will be generated, but caddy already cached
// certificates in its volume. This function resets that state.
func ResetCaddyState(caddyDataDir string) (err error) {
	defer derrors.Wrap(&err, "localca.ResetCaddyState(%q)", caddyDataDir)

	for _, sub := range []string{
		filepath.Join("caddy", "pki", "authorities", "local"),
		filepath.Join("caddy", "certificates", "local"),
	} {
		if err := os.RemoveAll(filepath.Join(caddyDataDir, sub)); err != nil {
			return err
		}
	}
	return nil
}

// parseCertAndKey validates that certPEM/keyPEM decode into a well-formed
// certificate and RSA private key, without otherwise doing anything with
// them -- just a sanity check so a corrupted or truncated CA on disk fails
// loudly instead of Caddy/dockerctl discovering it much later.
func parseCertAndKey(certPEM, keyPEM []byte) (*x509.Certificate, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("decode PEM certificate")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "RSA PRIVATE KEY" {
		return nil, fmt.Errorf("decode PEM private key")
	}
	if _, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes); err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return cert, nil
}
