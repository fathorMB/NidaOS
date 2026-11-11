// Package ca is the certificate authority of NidaOS (decision D20).
//
// The authority signs one server certificate for "*.<domain>" and
// "<domain>". Traefik gives this certificate for the addresses of the apps.
// The owner installs the certificate of the authority one time on each
// device. Then the browsers accept the addresses of the apps.
//
// The authority can sign only names in the domain (a name constraint).
// Thus a stolen key cannot make a certificate for an other site.
package ca

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// File names in the directory of the authority.
const (
	authorityCert = "ca.crt"
	authorityKey  = "ca.key"
	serverCert    = "server.crt"
	serverKey     = "server.key"
)

// Lifetimes of the certificates.
const (
	AuthorityLifetime = 20 * 365 * 24 * time.Hour
	// ServerLifetime is less than 398 days, the maximum that the Apple
	// systems accept for a server certificate.
	ServerLifetime = 397 * 24 * time.Hour
	// RenewBefore: the authority makes a new server certificate when the
	// current certificate ends in less than this time.
	RenewBefore = 30 * 24 * time.Hour
)

// Authority is the certificate authority in a directory.
type Authority struct {
	dir    string
	domain string
	now    func() time.Time

	mu      sync.Mutex
	cert    *x509.Certificate
	certPEM []byte
	key     crypto.Signer
}

// Open reads the authority in dir, or makes a new authority. Only root
// can read the key files.
func Open(dir, domain string) (*Authority, error) {
	domain = strings.TrimSuffix(strings.ToLower(domain), ".")
	if domain == "" {
		return nil, errors.New("the authority needs a domain")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	a := &Authority{dir: dir, domain: domain, now: time.Now}
	certPEM, err := os.ReadFile(filepath.Join(dir, authorityCert))
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := a.create(); err != nil {
			return nil, fmt.Errorf("make the authority: %w", err)
		}
		return a, nil
	case err != nil:
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, authorityKey))
	if err != nil {
		return nil, err
	}
	cert, err := parseCert(certPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", authorityCert, err)
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", authorityKey, err)
	}
	if !permits(cert, domain) {
		return nil, fmt.Errorf("the authority in %s is for an other domain: remove the directory to make a new authority", dir)
	}
	a.cert, a.certPEM, a.key = cert, certPEM, key
	return a, nil
}

func permits(cert *x509.Certificate, domain string) bool {
	return slices.Contains(cert.PermittedDNSDomains, domain)
}

func (a *Authority) create() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	now := a.now()
	tmpl := &x509.Certificate{
		SerialNumber:                serial(),
		Subject:                     pkix.Name{CommonName: "NidaOS local authority " + host, Organization: []string{"NidaOS"}},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.Add(AuthorityLifetime),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{a.domain},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := writeFile(filepath.Join(a.dir, authorityKey), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(a.dir, authorityCert), certPEM, 0o644); err != nil {
		return err
	}
	a.cert, a.certPEM, a.key = cert, certPEM, key
	return nil
}

// CertPEM gives the certificate of the authority. It is public data: the
// owner installs it on the devices.
func (a *Authority) CertPEM() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return bytes.Clone(a.certPEM)
}

// Fingerprint gives the SHA-256 fingerprint of the authority, as the
// browsers show it. The owner can compare it before the installation.
func (a *Authority) Fingerprint() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	sum := sha256.Sum256(a.cert.Raw)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(h)/2)
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// ServerCert gives the server certificate (with the authority) and its key
// in PEM. It makes a new certificate when there is none or when the
// certificate ends soon.
func (a *Authority) ServerCert() (certPEM, keyPEM []byte, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	certPath, keyPath := filepath.Join(a.dir, serverCert), filepath.Join(a.dir, serverKey)
	certPEM, err1 := os.ReadFile(certPath)
	keyPEM, err2 := os.ReadFile(keyPath)
	if err1 == nil && err2 == nil {
		if c, err := parseCert(certPEM); err == nil && a.now().Add(RenewBefore).Before(c.NotAfter) && c.CheckSignatureFrom(a.cert) == nil && c.VerifyHostname("x."+a.domain) == nil {
			return certPEM, keyPEM, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := a.now()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "*." + a.domain},
		DNSNames:     []string{"*." + a.domain, a.domain},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(ServerLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, key.Public(), a.key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	// The chain has the server certificate first, then the authority.
	certPEM = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), a.certPEM...)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := writeFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, nil, err
	}
	if err := writeFile(certPath, certPEM, 0o644); err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n.Add(n, big.NewInt(1))
}

func parseCert(b []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, errors.New("no certificate in PEM")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func parseKey(b []byte) (crypto.Signer, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("no key in PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, errors.New("the key cannot sign")
	}
	return s, nil
}

// writeFile writes a file with a rename, thus a stop cannot leave a half
// file.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
