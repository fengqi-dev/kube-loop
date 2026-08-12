package oidc

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
)

const (
	defaultHTTPTimeout = 10 * time.Second
	maxSecretBytes     = 64 << 10
)

type ClaimMapping struct {
	DisplayName string
	Email       string
	Groups      string
}

type Config struct {
	ID                 string
	DisplayName        string
	Issuer             string
	ClientID           string
	ClientSecret       string
	ClientSecretFile   string
	RedirectURL        string
	Scopes             []string
	AllowedSigningAlgs []string
	RequiredClaims     []string
	Claims             ClaimMapping
	HTTPTimeout        time.Duration
	RootCAs            *x509.CertPool
	HTTPClient         *http.Client
}

func (config Config) normalized() (Config, error) {
	config.ID = strings.TrimSpace(config.ID)
	config.DisplayName = strings.TrimSpace(config.DisplayName)
	// The issuer is a protocol identifier and must remain byte-for-byte equal to
	// the value advertised by OIDC discovery. Auth0 issuers commonly end in "/".
	config.Issuer = strings.TrimSpace(config.Issuer)
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.ClientSecretFile = strings.TrimSpace(config.ClientSecretFile)
	config.RedirectURL = strings.TrimSpace(config.RedirectURL)
	if config.ID == "" || config.ClientID == "" {
		return Config{}, errors.New("OIDC provider ID and client ID are required")
	}
	if err := requireHTTPSURL("OIDC issuer", config.Issuer, false); err != nil {
		return Config{}, err
	}
	if err := requireHTTPSURL("OIDC redirect URL", config.RedirectURL, true); err != nil {
		return Config{}, err
	}
	if config.ClientSecret != "" && config.ClientSecretFile != "" {
		return Config{}, errors.New("OIDC client secret and client secret file are mutually exclusive")
	}
	if config.ClientSecretFile != "" {
		secret, err := readSecretFile(config.ClientSecretFile)
		if err != nil {
			return Config{}, err
		}
		config.ClientSecret = secret
	}
	config.Scopes = normalizeUnique(config.Scopes)
	if !slices.Contains(config.Scopes, coreoidc.ScopeOpenID) {
		config.Scopes = append([]string{coreoidc.ScopeOpenID}, config.Scopes...)
	}
	if len(config.Scopes) == 1 {
		config.Scopes = append(config.Scopes, coreoidc.ScopeProfile, coreoidc.ScopeEmail)
	}
	config.AllowedSigningAlgs = normalizeUnique(config.AllowedSigningAlgs)
	if len(config.AllowedSigningAlgs) == 0 {
		config.AllowedSigningAlgs = []string{"RS256"}
	}
	for _, algorithm := range config.AllowedSigningAlgs {
		if !allowedAsymmetricAlgorithm(algorithm) {
			return Config{}, fmt.Errorf("OIDC signing algorithm %q is not allowed", algorithm)
		}
	}
	config.RequiredClaims = normalizeUnique(config.RequiredClaims)
	if len(config.RequiredClaims) == 0 {
		config.RequiredClaims = []string{"sub"}
	}
	config.Claims.DisplayName = defaultString(strings.TrimSpace(config.Claims.DisplayName), "name")
	config.Claims.Email = defaultString(strings.TrimSpace(config.Claims.Email), "email")
	config.Claims.Groups = defaultString(strings.TrimSpace(config.Claims.Groups), "groups")
	if config.HTTPTimeout <= 0 {
		config.HTTPTimeout = defaultHTTPTimeout
	}
	if config.HTTPClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if config.RootCAs != nil {
			transport.TLSClientConfig.RootCAs = config.RootCAs
		}
		config.HTTPClient = &http.Client{Transport: transport, Timeout: config.HTTPTimeout}
	} else if config.HTTPClient.Timeout <= 0 {
		copy := *config.HTTPClient
		copy.Timeout = config.HTTPTimeout
		config.HTTPClient = &copy
	}
	return config, nil
}

func requireHTTPSURL(name, value string, allowLoopbackHTTP bool) error {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", name)
	}
	loopback := parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"
	if parsed.Scheme != "https" && !(allowLoopbackHTTP && parsed.Scheme == "http" && loopback) {
		return fmt.Errorf("%s must use HTTPS", name)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must not contain user info, query parameters or a fragment", name)
	}
	return nil
}

func readSecretFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("read OIDC client secret file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSecretBytes {
		return "", errors.New("OIDC client secret file must be a small regular file")
	}
	data := make([]byte, info.Size())
	read, err := file.Read(data)
	if err != nil || int64(read) != info.Size() {
		return "", errors.New("read OIDC client secret file")
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", errors.New("OIDC client secret file is empty")
	}
	return secret, nil
}

func normalizeUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func allowedAsymmetricAlgorithm(algorithm string) bool {
	switch algorithm {
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA":
		return true
	default:
		return false
	}
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
