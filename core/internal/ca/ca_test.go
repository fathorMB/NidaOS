package ca

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerCertificateChainsToTheAuthority(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir, "nidaos.local")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := a.ServerCert()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(a.CertPEM())
	for _, host := range []string{"blog.nidaos.local", "nidaos.local"} {
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: pool}); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "example.com", Roots: pool}); err == nil {
		t.Error("the certificate is valid for example.com")
	}
	if leaf.NotAfter.Sub(leaf.NotBefore) > 398*24*time.Hour {
		t.Errorf("the server certificate is valid for %s", leaf.NotAfter.Sub(leaf.NotBefore))
	}

	// The same files give the same certificate.
	again, _, _ := a.ServerCert()
	if string(again) != string(certPEM) {
		t.Error("ServerCert made a new certificate without a need")
	}

	// A new Open reads the same authority.
	b, err := Open(dir, "nidaos.local")
	if err != nil {
		t.Fatal(err)
	}
	if b.Fingerprint() != a.Fingerprint() || len(a.Fingerprint()) != 95 {
		t.Errorf("fingerprint %q and %q", a.Fingerprint(), b.Fingerprint())
	}
	if _, err := Open(dir, "other.local"); err == nil {
		t.Error("Open accepted an authority of an other domain")
	}
}

func TestTheAuthorityCannotSignOtherDomains(t *testing.T) {
	a, err := Open(t.TempDir(), "nidaos.local")
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := parseCert(a.CertPEM())
	if !ca.PermittedDNSDomainsCritical || len(ca.PermittedDNSDomains) != 1 || ca.PermittedDNSDomains[0] != "nidaos.local" {
		t.Errorf("name constraint: %v", ca.PermittedDNSDomains)
	}
}

func TestServerCertificateIsRenewed(t *testing.T) {
	a, err := Open(t.TempDir(), "nidaos.local")
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := a.ServerCert()
	a.now = func() time.Time { return time.Now().Add(ServerLifetime - RenewBefore/2) }
	second, _, _ := a.ServerCert()
	if string(first) == string(second) {
		t.Error("the certificate is not renewed before its end")
	}
}

func TestKeyFileMode(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir, "nidaos.local")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = a.ServerCert()
	if os.PathSeparator == '\\' {
		return // Windows has no Unix modes
	}
	for _, f := range []string{authorityKey, serverKey} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, fi.Mode(), err)
		}
	}
}
