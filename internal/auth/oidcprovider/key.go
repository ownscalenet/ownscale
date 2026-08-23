package oidcprovider

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/ownscalenet/ownscale/internal/derrors"
)

// LoadOrGenerateSigningKey loads an RSA private key from path, or generates and
// persists a new 2048-bit key there if the file does not exist.
func LoadOrGenerateSigningKey(path string) (_ *rsa.PrivateKey, err error) {
	defer derrors.Wrap(&err, "oidcprovider.LoadOrGenerateSigningKey(%q)", path)

	keyPEM, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(keyPEM)
		if block == nil || block.Type != "RSA PRIVATE KEY" {
			return nil, fmt.Errorf("decode PEM private key")
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

type signingKey struct {
	id  string
	key *rsa.PrivateKey
}

func newSigningKey(key *rsa.PrivateKey) *signingKey {
	sum := sha256.Sum256(x509.MarshalPKCS1PublicKey(&key.PublicKey))
	return &signingKey{id: hex.EncodeToString(sum[:8]), key: key}
}

func (s *signingKey) SignatureAlgorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (s *signingKey) Key() any                                    { return s.key }
func (s *signingKey) ID() string                                  { return s.id }

type publicKey struct{ *signingKey }

func (p *publicKey) Algorithm() jose.SignatureAlgorithm { return p.SignatureAlgorithm() }
func (p *publicKey) Use() string                        { return "sig" }
func (p *publicKey) Key() any                           { return &p.key.PublicKey }
