package trafficinspect

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const (
	AuthorityCommonName  = "KubeLoop Traffic Inspection CA"
	authorityFileName    = "inspection-ca.pem"
	maximumAuthoritySize = 64 << 10
	pemCertificateType   = "CERTIFICATE"
	goosWindows          = "windows"
)

// Authority is a device-local certificate authority used only for traffic
// inspection. Its private key is never exposed as serialized bytes.
type Authority struct {
	certificate    tls.Certificate
	certificatePEM []byte
	fingerprint    string
}

// DefaultAuthorityPath returns the user-scoped CA bundle path.
func DefaultAuthorityPath() (string, error) {
	layout, err := utils.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.SecretsDir(), authorityFileName), nil
}

// LoadOrCreateAuthority loads an existing authority or generates a new one.
// Existing invalid, expired, or overly permissive files are rejected rather
// than replaced so a damaged trust identity cannot rotate silently.
func LoadOrCreateAuthority(path string) (*Authority, error) {
	return loadOrCreateAuthority(path, time.Now)
}

// TLSCertificate returns an independent certificate value suitable for
// Handler.Config.CA. Mutable certificate byte slices are copied so callers
// cannot alter the authority held by this object.
func (a *Authority) TLSCertificate() *tls.Certificate {
	if a == nil {
		return nil
	}
	certificate := a.certificate
	certificate.Certificate = cloneByteSlices(a.certificate.Certificate)
	certificate.SignedCertificateTimestamps = cloneByteSlices(a.certificate.SignedCertificateTimestamps)
	certificate.OCSPStaple = bytes.Clone(a.certificate.OCSPStaple)
	certificate.SupportedSignatureAlgorithms = append(
		[]tls.SignatureScheme(nil), a.certificate.SupportedSignatureAlgorithms...,
	)
	return &certificate
}

// PublicCertificatePEM returns the public CA certificate without its private key.
func (a *Authority) PublicCertificatePEM() []byte {
	if a == nil {
		return nil
	}
	return bytes.Clone(a.certificatePEM)
}

// FingerprintSHA256 returns the uppercase, unseparated SHA-256 fingerprint.
func (a *Authority) FingerprintSHA256() string {
	if a == nil {
		return ""
	}
	return a.fingerprint
}

func cloneByteSlices(values [][]byte) [][]byte {
	if values == nil {
		return nil
	}
	cloned := make([][]byte, len(values))
	for index, value := range values {
		cloned[index] = bytes.Clone(value)
	}
	return cloned
}

func loadOrCreateAuthority(path string, now func() time.Time) (*Authority, error) {
	path = filepath.Clean(path)
	if path == "." || !filepath.IsAbs(path) {
		return nil, errors.New("traffic inspection authority path must be absolute")
	}
	authority, err := loadAuthority(path, now())
	if err == nil {
		return authority, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	bundle, err := generateAuthority(now())
	if err != nil {
		return nil, err
	}
	if err := utils.WriteFile(path, bundle, 0o700, 0o600); err != nil {
		return nil, fmt.Errorf("persist traffic inspection authority: %w", err)
	}
	authority, err = loadAuthority(path, now())
	if err != nil {
		return nil, fmt.Errorf("load persisted traffic inspection authority: %w", err)
	}
	return authority, nil
}

func loadAuthority(path string, now time.Time) (*Authority, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect traffic inspection authority: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("traffic inspection authority must be a regular file")
	}
	if runtime.GOOS != goosWindows && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("traffic inspection authority permissions are %04o, want 0600", info.Mode().Perm())
	}
	if info.Size() <= 0 || info.Size() > maximumAuthoritySize {
		return nil, fmt.Errorf("traffic inspection authority size %d is invalid", info.Size())
	}
	bundle, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read traffic inspection authority: %w", err)
	}

	certificateBlock, remainder := pem.Decode(bundle)
	if certificateBlock == nil || certificateBlock.Type != pemCertificateType {
		return nil, errors.New("traffic inspection authority certificate PEM is invalid")
	}
	privateKeyBlock, trailing := pem.Decode(remainder)
	if privateKeyBlock == nil || privateKeyBlock.Type != "PRIVATE KEY" || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, errors.New("traffic inspection authority private key PEM is invalid")
	}
	certificatePEM := pem.EncodeToMemory(certificateBlock)
	privateKeyPEM := pem.EncodeToMemory(privateKeyBlock)
	tlsCertificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse traffic inspection authority key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse traffic inspection authority certificate: %w", err)
	}
	if leaf.Subject.CommonName != AuthorityCommonName || !leaf.IsCA || leaf.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("traffic inspection authority certificate constraints are invalid")
	}
	if err := leaf.CheckSignatureFrom(leaf); err != nil {
		return nil, fmt.Errorf("verify traffic inspection authority self-signature: %w", err)
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, errors.New("traffic inspection authority certificate is not currently valid")
	}
	tlsCertificate.Leaf = leaf
	fingerprint := sha256.Sum256(leaf.Raw)
	return &Authority{
		certificate:    tlsCertificate,
		certificatePEM: certificatePEM,
		fingerprint:    fmt.Sprintf("%X", fingerprint[:]),
	}, nil
}

func loadPublicAuthority(path string, now time.Time) (*Authority, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect public traffic inspection certificate: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumAuthoritySize {
		return nil, errors.New("public traffic inspection certificate file is invalid")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public traffic inspection certificate: %w", err)
	}
	block, trailing := pem.Decode(content)
	if block == nil || block.Type != pemCertificateType || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, errors.New("public traffic inspection certificate PEM is invalid")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public traffic inspection certificate: %w", err)
	}
	if leaf.Subject.CommonName != AuthorityCommonName || !leaf.IsCA || leaf.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("traffic inspection authority certificate constraints are invalid")
	}
	if err := leaf.CheckSignatureFrom(leaf); err != nil {
		return nil, fmt.Errorf("verify traffic inspection authority self-signature: %w", err)
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, errors.New("traffic inspection authority certificate is not currently valid")
	}
	certificatePEM := pem.EncodeToMemory(block)
	fingerprint := sha256.Sum256(leaf.Raw)
	return &Authority{
		certificate:    tls.Certificate{Certificate: [][]byte{leaf.Raw}, Leaf: leaf},
		certificatePEM: certificatePEM,
		fingerprint:    fmt.Sprintf("%X", fingerprint[:]),
	}, nil
}

func generateAuthority(now time.Time) ([]byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate traffic inspection authority key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("generate traffic inspection authority serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal traffic inspection authority public key: %w", err)
	}
	keyID := sha256.Sum256(publicKey)
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: AuthorityCommonName, Organization: []string{"KubeLoop"}},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SubjectKeyId:          bytes.Clone(keyID[:20]),
		AuthorityKeyId:        bytes.Clone(keyID[:20]),
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("create traffic inspection authority certificate: %w", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal traffic inspection authority private key: %w", err)
	}
	bundle := pem.EncodeToMemory(&pem.Block{Type: pemCertificateType, Bytes: certificateDER})
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})...)
	return bundle, nil
}
