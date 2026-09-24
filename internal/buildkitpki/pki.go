// Package buildkitpki manages the installation-local identities used to
// authenticate Stealth's worker, BuildKit daemon, and daemon health probe.
package buildkitpki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	DirectoryName = "buildkit-mtls"
	RenewBefore   = 30 * 24 * time.Hour
	caLifetime    = 10 * 365 * 24 * time.Hour
	leafLifetime  = 365 * 24 * time.Hour
	caRenewBefore = 365 * 24 * time.Hour
	serverName    = "buildkit"
	fileMode      = 0o644
	keyMode       = 0o600
	caKeyMode     = 0o600
	directoryMode = 0o700
)

var (
	ErrInvalidState = errors.New("BuildKit mTLS state is invalid")
)

type Paths struct {
	Root       string
	CACert     string
	CAKey      string
	ServerCert string
	ServerKey  string
	WorkerCert string
	WorkerKey  string
	HealthCert string
	HealthKey  string
}

func PathsAt(pkiDir string) Paths {
	return pathsAtRoot(filepath.Clean(pkiDir))
}

// Ensure creates an initial PKI, preserves a complete valid PKI, renews leaf
// identities within RenewBefore, and fails closed for inconsistent state.
// Host private keys remain mode 0600 under installation-private directories.
// One-shot Compose initializers copy only their assigned identity into
// service-specific volumes with service-owned read-only keys.
func Ensure(pkiDir string) (bool, error) {
	if pkiDir == "" || !filepath.IsAbs(pkiDir) || filepath.Clean(pkiDir) == string(filepath.Separator) {
		return false, fmt.Errorf("%w: invalid BuildKit PKI directory", ErrInvalidState)
	}
	pkiDir = filepath.Clean(pkiDir)
	parent := filepath.Dir(pkiDir)
	if err := ensureRealDirectory(parent, directoryMode, false); err != nil {
		return false, fmt.Errorf("inspect BuildKit PKI parent directory: %w", err)
	}
	if err := os.Chmod(parent, directoryMode); err != nil {
		return false, fmt.Errorf("protect BuildKit PKI parent directory: %w", err)
	}
	paths := PathsAt(pkiDir)
	pending := paths.Root + ".pending"
	previous := paths.Root + ".previous"

	if err := recoverBundle(paths, pending, previous); err != nil {
		return false, err
	}
	info, err := os.Lstat(paths.Root)
	if errors.Is(err, os.ErrNotExist) {
		if _, pendingErr := os.Lstat(pending); pendingErr == nil {
			if err := validateBundleOnly(pathsAtRoot(pending), false); err != nil {
				return false, fmt.Errorf("incomplete interrupted BuildKit mTLS issuance; repair the full PKI: %w", err)
			}
			if err := os.Rename(pending, paths.Root); err != nil {
				return false, fmt.Errorf("recover complete BuildKit mTLS issuance: %w", err)
			}
			if err := syncDirectory(parent); err != nil {
				return false, fmt.Errorf("sync recovered BuildKit PKI: %w", err)
			}
			return true, nil
		} else if !errors.Is(pendingErr, os.ErrNotExist) {
			return false, fmt.Errorf("inspect interrupted BuildKit PKI issuance: %w", pendingErr)
		}
		if err := writeBundleAtomic(paths, pending); err != nil {
			_ = os.RemoveAll(pending)
			return false, err
		}
		if err := os.Rename(pending, paths.Root); err != nil {
			return false, fmt.Errorf("publish BuildKit mTLS identity: %w", err)
		}
		if err := syncDirectory(parent); err != nil {
			return false, fmt.Errorf("sync BuildKit PKI: %w", err)
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect BuildKit mTLS identity: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != directoryMode {
		return false, fmt.Errorf("%w: BuildKit mTLS directory must be a real mode-0700 directory", ErrInvalidState)
	}
	rotateCA, rotateLeaves, err := validateBundle(paths, true)
	if err != nil {
		return false, err
	}
	if !rotateCA && !rotateLeaves {
		return false, nil
	}
	if rotateCA {
		if err := writeBundleAtomic(paths, pending); err != nil {
			_ = os.RemoveAll(pending)
			return false, err
		}
	} else if err := writeLeafRenewalBundle(paths, pending); err != nil {
		_ = os.RemoveAll(pending)
		return false, err
	}
	if err := replaceBundle(paths.Root, pending, previous, parent); err != nil {
		return false, err
	}
	return true, nil
}

// ValidateExisting checks a complete installed bundle without creating or
// rotating any identity. The installer uses it before atomically relocating a
// development bundle from the legacy state directory.
func ValidateExisting(pkiDir string) error {
	if pkiDir == "" || !filepath.IsAbs(pkiDir) || filepath.Clean(pkiDir) == string(filepath.Separator) {
		return fmt.Errorf("%w: invalid BuildKit PKI directory", ErrInvalidState)
	}
	return validateBundleOnly(PathsAt(pkiDir), false)
}

// ValidateForRelocation validates a complete bundle while permitting expired
// leaves that Ensure can renew after the bundle has moved to its new location.
func ValidateForRelocation(pkiDir string) error {
	if pkiDir == "" || !filepath.IsAbs(pkiDir) || filepath.Clean(pkiDir) == string(filepath.Separator) {
		return fmt.Errorf("%w: invalid BuildKit PKI directory", ErrInvalidState)
	}
	_, _, err := validateBundle(PathsAt(pkiDir), true)
	return err
}

func recoverBundle(paths Paths, pending, previous string) error {
	_, rootErr := os.Lstat(paths.Root)
	previousInfo, previousErr := os.Lstat(previous)
	if previousErr == nil && (previousInfo.Mode()&os.ModeSymlink != 0 || !previousInfo.IsDir()) {
		return fmt.Errorf("%w: interrupted BuildKit mTLS backup is not a real directory", ErrInvalidState)
	}
	if rootErr == nil {
		if err := validateBundleOnly(paths, true); err != nil {
			return err
		}
		if previousErr == nil {
			if err := os.RemoveAll(previous); err != nil {
				return fmt.Errorf("remove recovered BuildKit mTLS backup: %w", err)
			}
		}
		if _, err := os.Lstat(pending); err == nil {
			pendingPaths := pathsAtRoot(pending)
			if pendingInfo, statErr := os.Lstat(pending); statErr != nil || pendingInfo.Mode()&os.ModeSymlink != 0 || !pendingInfo.IsDir() {
				return fmt.Errorf("%w: interrupted BuildKit mTLS staging path is unsafe", ErrInvalidState)
			} else if validateErr := validateBundleOnly(pendingPaths, false); validateErr == nil {
				if err := os.RemoveAll(pending); err != nil {
					return fmt.Errorf("remove completed BuildKit mTLS staging bundle: %w", err)
				}
			} else {
				return fmt.Errorf("%w: interrupted BuildKit mTLS staging bundle is incomplete", ErrInvalidState)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect interrupted BuildKit mTLS staging bundle: %w", err)
		}
		return nil
	}
	if !errors.Is(rootErr, os.ErrNotExist) {
		return fmt.Errorf("inspect BuildKit mTLS identity: %w", rootErr)
	}
	if previousErr == nil {
		previousPaths := pathsAtRoot(previous)
		if err := validateBundleOnly(previousPaths, true); err != nil {
			return fmt.Errorf("%w: interrupted BuildKit mTLS rotation has no valid active or previous bundle", err)
		}
		if pendingInfo, pendingErr := os.Lstat(pending); pendingErr == nil {
			if pendingInfo.Mode()&os.ModeSymlink != 0 || !pendingInfo.IsDir() {
				return fmt.Errorf("%w: interrupted BuildKit mTLS staging path is unsafe", ErrInvalidState)
			}
			if err := validateBundleOnly(pathsAtRoot(pending), false); err != nil {
				return fmt.Errorf("%w: interrupted BuildKit mTLS replacement is incomplete", err)
			}
			if err := os.Rename(pending, paths.Root); err != nil {
				return fmt.Errorf("finish BuildKit mTLS rotation: %w", err)
			}
		} else if errors.Is(pendingErr, os.ErrNotExist) {
			if err := os.Rename(previous, paths.Root); err != nil {
				return fmt.Errorf("restore previous BuildKit mTLS identity: %w", err)
			}
		} else {
			return fmt.Errorf("inspect interrupted BuildKit mTLS replacement: %w", pendingErr)
		}
		if err := os.RemoveAll(previous); err != nil {
			return fmt.Errorf("remove previous BuildKit mTLS identity: %w", err)
		}
		return syncDirectory(filepath.Dir(paths.Root))
	}
	if !errors.Is(previousErr, os.ErrNotExist) {
		return fmt.Errorf("inspect previous BuildKit mTLS identity: %w", previousErr)
	}
	return nil
}

func writeBundleAtomic(paths Paths, destination string) error {
	parent := filepath.Dir(paths.Root)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create BuildKit mTLS parent: %w", err)
	}
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: BuildKit mTLS parent must be a real directory", ErrInvalidState)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("%w: refusing to overwrite an existing BuildKit mTLS staging path", ErrInvalidState)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect BuildKit mTLS staging path: %w", err)
	}
	if err := prepareBundleDirectories(destination); err != nil {
		return err
	}
	paths = pathsAtRoot(destination)
	caKey, caCert, err := createCA(time.Now().UTC())
	if err != nil {
		return err
	}
	if err := writePEM(paths.CACert, "CERTIFICATE", caCert.Raw, fileMode); err != nil {
		return err
	}
	if err := writeKey(paths.CAKey, caKey, caKeyMode); err != nil {
		return err
	}
	for _, leaf := range []struct {
		cert string
		key  string
		role role
	}{
		{paths.ServerCert, paths.ServerKey, serverRole},
		{paths.WorkerCert, paths.WorkerKey, clientRole},
		{paths.HealthCert, paths.HealthKey, healthRole},
	} {
		key, cert, err := createLeaf(caKey, caCert, leaf.role, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := writePEM(leaf.cert, "CERTIFICATE", cert.Raw, fileMode); err != nil {
			return err
		}
		if err := writeKey(leaf.key, key, keyMode); err != nil {
			return err
		}
	}
	if err := syncTreeDirectories(paths); err != nil {
		return err
	}
	if err := validateBundleOnly(paths, false); err != nil {
		return fmt.Errorf("validate generated BuildKit mTLS identity: %w", err)
	}
	return nil
}

func writeLeafRenewalBundle(current Paths, destination string) error {
	if err := prepareBundleDirectories(destination); err != nil {
		return err
	}
	paths := pathsAtRoot(destination)
	caCertPEM, err := readRegular(current.CACert, fileMode)
	if err != nil {
		return invalidFile("BuildKit CA certificate", err)
	}
	caKeyPEM, err := readRegular(current.CAKey, caKeyMode)
	if err != nil {
		return invalidFile("BuildKit CA private key", err)
	}
	caBlock, _ := pem.Decode(caCertPEM)
	if caBlock == nil {
		return invalidFile("BuildKit CA certificate", errors.New("invalid PEM"))
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return invalidFile("BuildKit CA certificate", err)
	}
	caKey, err := parsePrivateKey(caKeyPEM)
	if err != nil || !publicKeysMatch(caCert.PublicKey, caKey) {
		return invalidFile("BuildKit CA private key", errors.New("certificate and private key do not match"))
	}
	if err := writePEM(paths.CACert, "CERTIFICATE", caCert.Raw, fileMode); err != nil {
		return err
	}
	if err := writeKey(paths.CAKey, caKey, caKeyMode); err != nil {
		return err
	}
	for _, leaf := range []struct {
		cert string
		key  string
		role role
	}{
		{paths.ServerCert, paths.ServerKey, serverRole},
		{paths.WorkerCert, paths.WorkerKey, clientRole},
		{paths.HealthCert, paths.HealthKey, healthRole},
	} {
		key, cert, err := createLeaf(caKey, caCert, leaf.role, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := writePEM(leaf.cert, "CERTIFICATE", cert.Raw, fileMode); err != nil {
			return err
		}
		if err := writeKey(leaf.key, key, keyMode); err != nil {
			return err
		}
	}
	if err := syncTreeDirectories(paths); err != nil {
		return err
	}
	if err := validateBundleOnly(paths, false); err != nil {
		return fmt.Errorf("validate renewed BuildKit leaf identities: %w", err)
	}
	return nil
}

func prepareBundleDirectories(destination string) error {
	if err := os.Mkdir(destination, directoryMode); err != nil {
		return fmt.Errorf("create BuildKit mTLS staging directory: %w", err)
	}
	if err := os.Chmod(destination, directoryMode); err != nil {
		return fmt.Errorf("protect BuildKit mTLS staging directory: %w", err)
	}
	paths := pathsAtRoot(destination)
	for _, dir := range []string{filepath.Dir(paths.ServerCert), filepath.Dir(paths.WorkerCert), filepath.Dir(paths.HealthCert)} {
		if err := os.Mkdir(dir, directoryMode); err != nil {
			return fmt.Errorf("create BuildKit mTLS identity directory: %w", err)
		}
		if err := os.Chmod(dir, directoryMode); err != nil {
			return fmt.Errorf("protect BuildKit mTLS identity directory: %w", err)
		}
	}
	return nil
}

func replaceBundle(current, pending, previous, parent string) error {
	if err := os.Rename(current, previous); err != nil {
		return fmt.Errorf("stage previous BuildKit mTLS identity: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("sync staged BuildKit mTLS identity: %w", err)
	}
	if err := os.Rename(pending, current); err != nil {
		_ = os.Rename(previous, current)
		_ = syncDirectory(parent)
		return fmt.Errorf("activate renewed BuildKit mTLS identity: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("sync renewed BuildKit mTLS identity: %w", err)
	}
	if err := os.RemoveAll(previous); err != nil {
		return fmt.Errorf("remove previous BuildKit mTLS identity: %w", err)
	}
	return syncDirectory(parent)
}

func pathsAtRoot(root string) Paths {
	return Paths{Root: root, CACert: filepath.Join(root, "ca-cert.pem"), CAKey: filepath.Join(root, "ca-key.pem"), ServerCert: filepath.Join(root, "server", "cert.pem"), ServerKey: filepath.Join(root, "server", "key.pem"), WorkerCert: filepath.Join(root, "worker", "cert.pem"), WorkerKey: filepath.Join(root, "worker", "key.pem"), HealthCert: filepath.Join(root, "health", "cert.pem"), HealthKey: filepath.Join(root, "health", "key.pem")}
}

func validateBundle(paths Paths, allowRenewal bool) (bool, bool, error) {
	if err := ensureRealDirectory(paths.Root, directoryMode, true); err != nil {
		return false, false, fmt.Errorf("%w: BuildKit mTLS directory: %v", ErrInvalidState, err)
	}
	bundleUID, err := ownerUID(paths.Root)
	if err != nil {
		return false, false, fmt.Errorf("%w: BuildKit mTLS directory owner is invalid", ErrInvalidState)
	}
	for _, dir := range []string{filepath.Dir(paths.ServerCert), filepath.Dir(paths.WorkerCert), filepath.Dir(paths.HealthCert)} {
		if err := ensureRealDirectory(dir, directoryMode, true); err != nil {
			return false, false, fmt.Errorf("%w: BuildKit mTLS identity directory is missing or unsafe", ErrInvalidState)
		}
		if err := requireOwnerUID(dir, bundleUID); err != nil {
			return false, false, fmt.Errorf("%w: BuildKit mTLS identity directory owner is inconsistent", ErrInvalidState)
		}
	}
	caPEM, err := readRegular(paths.CACert, fileMode)
	if err != nil {
		return false, false, invalidFile("BuildKit CA certificate", err)
	}
	if err := requireOwnerUID(paths.CACert, bundleUID); err != nil {
		return false, false, invalidFile("BuildKit CA certificate", err)
	}
	caKeyPEM, err := readRegular(paths.CAKey, caKeyMode)
	if err != nil {
		return false, false, invalidFile("BuildKit CA private key", err)
	}
	if err := requireOwnerUID(paths.CAKey, bundleUID); err != nil {
		return false, false, invalidFile("BuildKit CA private key", err)
	}
	caBlock, rest := pem.Decode(caPEM)
	if caBlock == nil || caBlock.Type != "CERTIFICATE" || strings.TrimSpace(string(rest)) != "" {
		return false, false, invalidFile("BuildKit CA certificate", errors.New("invalid PEM"))
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil || !caCert.IsCA || !caCert.BasicConstraintsValid || caCert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return false, false, invalidFile("BuildKit CA certificate", errors.New("CA constraints or key usage are invalid"))
	}
	caKey, err := parsePrivateKey(caKeyPEM)
	if err != nil || !publicKeysMatch(caCert.PublicKey, caKey) {
		return false, false, invalidFile("BuildKit CA private key", errors.New("certificate and private key do not match"))
	}
	now := time.Now().UTC()
	if now.Before(caCert.NotBefore) {
		return false, false, invalidFile("BuildKit CA certificate", errors.New("certificate is not yet valid"))
	}
	rotateCA := !now.Before(caCert.NotAfter) || time.Until(caCert.NotAfter) <= caRenewBefore
	rotateLeaves := false
	for _, leaf := range []struct {
		name     string
		certPath string
		keyPath  string
		role     role
	}{
		{"server", paths.ServerCert, paths.ServerKey, serverRole},
		{"worker client", paths.WorkerCert, paths.WorkerKey, clientRole},
		{"health client", paths.HealthCert, paths.HealthKey, healthRole},
	} {
		certPEM, err := readRegular(leaf.certPath, fileMode)
		if err != nil {
			return false, false, invalidFile("BuildKit "+leaf.name+" certificate", err)
		}
		if err := requireOwnerUID(leaf.certPath, bundleUID); err != nil {
			return false, false, invalidFile("BuildKit "+leaf.name+" certificate", err)
		}
		keyPEM, err := readRegular(leaf.keyPath, keyMode)
		if err != nil {
			return false, false, invalidFile("BuildKit "+leaf.name+" private key", err)
		}
		if err := requireOwnerUID(leaf.keyPath, bundleUID); err != nil {
			return false, false, invalidFile("BuildKit "+leaf.name+" private key", err)
		}
		block, trailing := pem.Decode(certPEM)
		if block == nil || block.Type != "CERTIFICATE" || strings.TrimSpace(string(trailing)) != "" {
			return false, false, invalidFile("BuildKit "+leaf.name+" certificate", errors.New("invalid PEM"))
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return false, false, invalidFile("BuildKit "+leaf.name+" certificate", err)
		}
		key, err := parsePrivateKey(keyPEM)
		if err != nil || !publicKeysMatch(cert.PublicKey, key) {
			return false, false, invalidFile("BuildKit "+leaf.name+" private key", errors.New("certificate and private key do not match"))
		}
		if err := validateLeaf(cert, caCert, leaf.role, now, allowRenewal); err != nil {
			return false, false, invalidFile("BuildKit "+leaf.name+" certificate", err)
		}
		if !now.Before(cert.NotAfter) || time.Until(cert.NotAfter) <= RenewBefore {
			rotateLeaves = true
		}
	}
	if rotateCA {
		return true, true, nil
	}
	return false, rotateLeaves, nil
}

func validateBundleOnly(paths Paths, allowRenewal bool) error {
	_, _, err := validateBundle(paths, allowRenewal)
	return err
}

func validateLeaf(cert, ca *x509.Certificate, expected role, now time.Time, allowRenewal bool) error {
	if cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return errors.New("leaf constraints or key usage are invalid")
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != expected.usage {
		return errors.New("extended key usage does not match the identity role")
	}
	if expected.dnsName != "" {
		if err := cert.VerifyHostname(expected.dnsName); err != nil {
			return errors.New("server certificate DNS SAN is invalid")
		}
		if len(cert.DNSNames) != 1 || cert.DNSNames[0] != expected.dnsName {
			return errors.New("server certificate contains an unexpected DNS SAN")
		}
	} else if len(cert.DNSNames) != 0 || len(cert.IPAddresses) != 0 {
		return errors.New("client identity must not carry server names")
	}
	if !now.Before(cert.NotBefore) {
		// Continue below. A leaf not yet valid may be an operator clock issue;
		// a just-expired leaf is the documented renewable condition.
	} else {
		return errors.New("certificate is not yet valid")
	}
	if now.Before(cert.NotAfter) {
		// Valid leaf.
	} else if !allowRenewal {
		return errors.New("certificate has expired")
	}
	if err := cert.CheckSignatureFrom(ca); err != nil {
		return errors.New("certificate is not signed by the Stealth BuildKit CA")
	}
	return nil
}

type role struct {
	usage   x509.ExtKeyUsage
	dnsName string
	name    string
}

var (
	serverRole = role{usage: x509.ExtKeyUsageServerAuth, dnsName: serverName, name: "stealth-buildkit-server"}
	clientRole = role{usage: x509.ExtKeyUsageClientAuth, name: "stealth-buildkit-worker"}
	healthRole = role{usage: x509.ExtKeyUsageClientAuth, name: "stealth-buildkit-health"}
)

func createCA(now time.Time) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate BuildKit CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Stealth BuildKit CA", Organization: []string{"Stealth"}},
		NotBefore:    now.Add(-5 * time.Minute), NotAfter: now.Add(caLifetime),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		SubjectKeyId: make([]byte, 20),
	}
	if _, err := rand.Read(template.SubjectKeyId); err != nil {
		return nil, nil, fmt.Errorf("generate BuildKit CA subject key ID: %w", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, nil, fmt.Errorf("issue BuildKit CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated BuildKit CA certificate: %w", err)
	}
	return key, cert, nil
}

func createLeaf(caKey *ecdsa.PrivateKey, ca *x509.Certificate, identity role, now time.Time) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate BuildKit %s key: %w", identity.name, err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: identity.name, Organization: []string{"Stealth"}},
		NotBefore:    now.Add(-5 * time.Minute), NotAfter: now.Add(leafLifetime),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{identity.usage},
	}
	if identity.dnsName != "" {
		template.DNSNames = []string{identity.dnsName}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, key.Public(), caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("issue BuildKit %s certificate: %w", identity.name, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated BuildKit %s certificate: %w", identity.name, err)
	}
	return key, cert, nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 159)
	for {
		serial, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return nil, fmt.Errorf("generate BuildKit certificate serial: %w", err)
		}
		if serial.Sign() > 0 {
			return serial, nil
		}
	}
}

func writeKey(path string, key *ecdsa.PrivateKey, mode os.FileMode) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal BuildKit private key: %w", err)
	}
	return writePEM(path, "PRIVATE KEY", der, mode)
}

func writePEM(path, kind string, contents []byte, mode os.FileMode) error {
	return atomicWrite(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: contents}), mode)
}

func atomicWrite(path string, contents []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
		return fmt.Errorf("create BuildKit mTLS file directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".stealth-buildkit-*")
	if err != nil {
		return fmt.Errorf("stage BuildKit mTLS file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect BuildKit mTLS file: %w", err)
	}
	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write BuildKit mTLS file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync BuildKit mTLS file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close BuildKit mTLS file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish BuildKit mTLS file: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func readRegular(path string, expectedMode os.FileMode) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	if info.Mode().Perm() != expectedMode {
		return nil, fmt.Errorf("file mode is %04o, expected %04o", info.Mode().Perm(), expectedMode)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return contents, nil
}

func ownerUID(path string) (uint32, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("owner cannot be read from an unsafe path")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("filesystem does not report a numeric owner")
	}
	return stat.Uid, nil
}

func requireOwnerUID(path string, expected uint32) error {
	actual, err := ownerUID(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("owner does not match the private BuildKit PKI directory")
	}
	return nil
}

func ensureRealDirectory(path string, mode os.FileMode, mustExist bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && !mustExist {
		return os.MkdirAll(path, mode)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("not a real directory")
	}
	if mustExist && info.Mode().Perm() != mode {
		return fmt.Errorf("directory mode is %04o, expected %04o", info.Mode().Perm(), mode)
	}
	return nil
}

func parsePrivateKey(contents []byte) (*ecdsa.PrivateKey, error) {
	block, rest := pem.Decode(contents)
	if block == nil || block.Type != "PRIVATE KEY" || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("invalid PKCS#8 private key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("private key must use ECDSA P-256")
	}
	return key, nil
}

func publicKeysMatch(certPublic any, key *ecdsa.PrivateKey) bool {
	certKey, ok := certPublic.(*ecdsa.PublicKey)
	return ok && certKey.Curve == key.Curve && certKey.X.Cmp(key.X) == 0 && certKey.Y.Cmp(key.Y) == 0
}

func invalidFile(name string, err error) error {
	return fmt.Errorf("%w: %s is missing, malformed, or inconsistent (%v)", ErrInvalidState, name, err)
}

func syncTreeDirectories(paths Paths) error {
	for _, dir := range []string{filepath.Dir(paths.ServerCert), filepath.Dir(paths.WorkerCert), filepath.Dir(paths.HealthCert), paths.Root} {
		if err := syncDirectory(dir); err != nil {
			return fmt.Errorf("sync BuildKit mTLS directory: %w", err)
		}
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
