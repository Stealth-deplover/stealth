package buildkitpki

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureCreatesDistinctRoleBoundIdentitiesAndPreservesThem(t *testing.T) {
	state := testPKIDir(t)
	changed, err := Ensure(state)
	if err != nil || !changed {
		t.Fatalf("initial Ensure = %v, %v", changed, err)
	}
	paths := PathsAt(state)
	if paths.Root != state {
		t.Fatalf("PathsAt(%q).Root = %q; the argument must be the PKI directory itself", state, paths.Root)
	}
	caPEM := read(t, paths.CACert)
	caBlock, _ := pem.Decode(caPEM)
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil || !ca.IsCA || !ca.BasicConstraintsValid || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("CA constraints are invalid: cert=%+v err=%v", ca, err)
	}
	if mode(t, paths.CAKey) != caKeyMode || mode(t, paths.ServerKey) != keyMode || mode(t, paths.WorkerKey) != keyMode || mode(t, paths.HealthKey) != keyMode {
		t.Fatal("unexpected private key file permissions")
	}
	serverCert, serverKey := identity(t, paths.ServerCert, paths.ServerKey)
	workerCert, workerKey := identity(t, paths.WorkerCert, paths.WorkerKey)
	healthCert, healthKey := identity(t, paths.HealthCert, paths.HealthKey)
	assertRole(t, serverCert, ca, serverRole)
	assertRole(t, workerCert, ca, clientRole)
	assertRole(t, healthCert, ca, healthRole)
	assertDistinct(t, serverKey, workerKey, healthKey)
	if serverCert.SerialNumber.Cmp(workerCert.SerialNumber) == 0 || serverCert.SerialNumber.Cmp(healthCert.SerialNumber) == 0 || workerCert.SerialNumber.Cmp(healthCert.SerialNumber) == 0 {
		t.Fatal("leaf certificate serials are not distinct")
	}
	before := map[string][]byte{}
	for _, path := range []string{paths.CACert, paths.CAKey, paths.ServerCert, paths.ServerKey, paths.WorkerCert, paths.WorkerKey, paths.HealthCert, paths.HealthKey} {
		before[path] = read(t, path)
	}
	changed, err = Ensure(state)
	if err != nil || changed {
		t.Fatalf("second Ensure = %v, %v; expected idempotent preserve", changed, err)
	}
	for path, want := range before {
		if got := read(t, path); string(got) != string(want) {
			t.Fatalf("idempotent Ensure changed %s", filepath.Base(path))
		}
	}
}

func TestEnsureRenewsLeafSetBeforeExpiryWithoutRotatingCA(t *testing.T) {
	state := testPKIDir(t)
	if _, err := Ensure(state); err != nil {
		t.Fatal(err)
	}
	paths := PathsAt(state)
	oldCA := read(t, paths.CACert)
	caKey, caCert := caIdentity(t, paths)
	_, workerKey := identity(t, paths.WorkerCert, paths.WorkerKey)
	writeLeafForTest(t, paths.WorkerCert, workerKey, caKey, caCert, clientRole, time.Now().Add(-5*24*time.Hour), time.Now().Add(10*24*time.Hour))
	changed, err := Ensure(state)
	if err != nil || !changed {
		t.Fatalf("renewing leaf Ensure = %v, %v", changed, err)
	}
	if string(read(t, paths.CACert)) != string(oldCA) {
		t.Fatal("leaf renewal changed the CA identity")
	}
	workerCert, _ := identity(t, paths.WorkerCert, paths.WorkerKey)
	if time.Until(workerCert.NotAfter) <= RenewBefore {
		t.Fatalf("worker certificate was not renewed: expires %s", workerCert.NotAfter)
	}
	if _, err := Ensure(state); err != nil {
		t.Fatalf("renewed bundle does not validate: %v", err)
	}
}

func TestEnsureRenewsExpiredLeafAndRejectsBrokenState(t *testing.T) {
	t.Run("expired leaf is renewed", func(t *testing.T) {
		state := testPKIDir(t)
		if _, err := Ensure(state); err != nil {
			t.Fatal(err)
		}
		paths := PathsAt(state)
		caKey, caCert := caIdentity(t, paths)
		_, workerKey := identity(t, paths.WorkerCert, paths.WorkerKey)
		writeLeafForTest(t, paths.WorkerCert, workerKey, caKey, caCert, clientRole, time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour))
		changed, err := Ensure(state)
		if err != nil || !changed {
			t.Fatalf("expired leaf Ensure = %v, %v", changed, err)
		}
	})
	for name, mutate := range map[string]func(*testing.T, Paths){
		"missing member": func(t *testing.T, paths Paths) { mustRemove(t, paths.WorkerKey) },
		"truncated PEM": func(t *testing.T, paths Paths) {
			mustWrite(t, paths.ServerCert, []byte("-----BEGIN CERTIFICATE-----\nbroken\n"), fileMode)
		},
		"certificate key mismatch": func(t *testing.T, paths Paths) { mustWrite(t, paths.ServerKey, read(t, paths.WorkerKey), keyMode) },
		"wrong CA": func(t *testing.T, paths Paths) {
			otherKey, otherCA, err := createCA(time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			_, serverKey := identity(t, paths.ServerCert, paths.ServerKey)
			_, cert := makeLeafForTest(t, serverKey, otherKey, otherCA, serverRole, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
			mustWrite(t, paths.ServerCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), fileMode)
		},
		"wrong SAN": func(t *testing.T, paths Paths) {
			caKey, caCert := caIdentity(t, paths)
			_, serverKey := identity(t, paths.ServerCert, paths.ServerKey)
			wrongRole := serverRole
			wrongRole.dnsName = "other"
			_, cert := makeLeafForTest(t, serverKey, caKey, caCert, wrongRole, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
			mustWrite(t, paths.ServerCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), fileMode)
		},
		"wrong EKU": func(t *testing.T, paths Paths) {
			caKey, caCert := caIdentity(t, paths)
			_, workerKey := identity(t, paths.WorkerCert, paths.WorkerKey)
			wrongRole := clientRole
			wrongRole.usage = x509.ExtKeyUsageServerAuth
			_, cert := makeLeafForTest(t, workerKey, caKey, caCert, wrongRole, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
			mustWrite(t, paths.WorkerCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), fileMode)
		},
		"key symlink": func(t *testing.T, paths Paths) {
			mustRemove(t, paths.ServerKey)
			if err := os.Symlink(paths.WorkerKey, paths.ServerKey); err != nil {
				t.Fatal(err)
			}
		},
		"wrong permissions": func(t *testing.T, paths Paths) {
			if err := os.Chmod(paths.ServerKey, 0o660); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := testPKIDir(t)
			if _, err := Ensure(state); err != nil {
				t.Fatal(err)
			}
			mutate(t, PathsAt(state))
			if _, err := Ensure(state); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("corrupt PKI accepted: %v", err)
			}
		})
	}
}

func TestEnsureRecoversCompleteInterruptedCreationAndFailsOnPartial(t *testing.T) {
	state := testPKIDir(t)
	paths := PathsAt(state)
	pending := paths.Root + ".pending"
	if err := writeBundleAtomic(paths, pending); err != nil {
		t.Fatal(err)
	}
	changed, err := Ensure(state)
	if err != nil || !changed {
		t.Fatalf("complete interrupted issuance recovery = %v, %v", changed, err)
	}
	if _, err := Ensure(state); err != nil {
		t.Fatalf("recovered bundle does not validate: %v", err)
	}

	state = testPKIDir(t)
	paths = PathsAt(state)
	pending = paths.Root + ".pending"
	if err := os.MkdirAll(filepath.Dir(pending), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(pending, directoryMode); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("partial issuance was not rejected: %v", err)
	}
}

func TestEnsureRejectsPKIDirectorySymlink(t *testing.T) {
	parent := t.TempDir()
	actual := filepath.Join(parent, "actual")
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "state-link")
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(link); err == nil {
		t.Fatal("symlink PKI directory accepted")
	}
}

func TestProductionComposeSmokePreparePKI(t *testing.T) {
	pkiDir := os.Getenv("STEALTH_BUILDKIT_PKI_SMOKE_DIR")
	if pkiDir == "" {
		t.Skip("production Compose smoke did not request host PKI preparation")
	}
	if _, err := Ensure(pkiDir); err != nil {
		t.Fatalf("prepare production Compose smoke BuildKit PKI: %v", err)
	}
}

func testPKIDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "private", DirectoryName)
}

func assertRole(t *testing.T, cert, ca *x509.Certificate, expected role) {
	t.Helper()
	if err := validateLeaf(cert, ca, expected, time.Now().UTC(), false); err != nil {
		t.Fatalf("invalid %s role: %v", expected.name, err)
	}
}

func assertDistinct(t *testing.T, a, b, c *ecdsa.PrivateKey) {
	t.Helper()
	if a.X.Cmp(b.X) == 0 && a.Y.Cmp(b.Y) == 0 || a.X.Cmp(c.X) == 0 && a.Y.Cmp(c.Y) == 0 || b.X.Cmp(c.X) == 0 && b.Y.Cmp(c.Y) == 0 {
		t.Fatal("TLS identities reused an ECDSA key")
	}
}

func identity(t *testing.T, certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	certPEM := read(t, certPath)
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatalf("no certificate PEM in %s", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, err := parsePrivateKey(read(t, keyPath))
	if err != nil {
		t.Fatal(err)
	}
	if !publicKeysMatch(cert.PublicKey, key) {
		t.Fatalf("certificate and key mismatch for %s", certPath)
	}
	return cert, key
}

func caIdentity(t *testing.T, paths Paths) (*ecdsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	caKey, err := parsePrivateKey(read(t, paths.CAKey))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(read(t, paths.CACert))
	if block == nil {
		t.Fatal("missing CA certificate")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return caKey, caCert
}

func makeLeafForTest(t *testing.T, leafKey, caKey *ecdsa.PrivateKey, ca *x509.Certificate, expected role, notBefore, notAfter time.Time) (*ecdsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: expected.name},
		NotBefore:    notBefore, NotAfter: notAfter,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{expected.usage},
	}
	if expected.dnsName != "" {
		template.DNSNames = []string{expected.dnsName}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, leafKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return leafKey, cert
}

func writeLeafForTest(t *testing.T, path string, key, caKey *ecdsa.PrivateKey, ca *x509.Certificate, expected role, notBefore, notAfter time.Time) {
	t.Helper()
	_, cert := makeLeafForTest(t, key, caKey, ca, expected, notBefore, notAfter)
	mustWrite(t, path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), fileMode)
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func mustWrite(t *testing.T, path string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
