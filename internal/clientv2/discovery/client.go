package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	version "github.com/hashicorp/go-version"
)

const (
	Path                         = "/.well-known/kubeloop"
	DefaultProtocolVersion       = "2.0"
	DefaultTunnelPath            = "/tunnel"
	DefaultTimeout               = 10 * time.Second
	MaxDocumentBytes             = 64 << 10
	CodeVersionMismatch          = "VERSION_MISMATCH"
	CodeClientVersionUnsupported = "CLIENT_VERSION_UNSUPPORTED"
)

type AuthMethod struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"displayName,omitempty"`
	Interaction string `json:"interaction"`
}

type Document struct {
	ServiceID        string       `json:"serviceId"`
	PublicURL        string       `json:"publicUrl"`
	TunnelPath       string       `json:"tunnelPath"`
	APIVersions      []string     `json:"apiVersions"`
	AuthMethods      []AuthMethod `json:"authMethods"`
	Features         []string     `json:"features"`
	ServerVersion    string       `json:"serverVersion"`
	ServerCommit     string       `json:"serverCommit,omitempty"`
	ProtocolMin      string       `json:"protocolMin"`
	ProtocolMax      string       `json:"protocolMax"`
	MinClientVersion string       `json:"minClientVersion,omitempty"`
}

// CompatibilityError reports a stable incompatibility before login or WSS
// setup. It is safe for UI code to branch on Code without parsing Error().
type CompatibilityError struct {
	Code            string
	Message         string
	ClientVersion   string
	ProtocolVersion string
	Minimum         string
	Maximum         string
}

func (compatibilityError *CompatibilityError) Error() string {
	if compatibilityError == nil {
		return ""
	}
	return compatibilityError.Message
}

type Config struct {
	HTTPClient      *http.Client
	Timeout         time.Duration
	ClientVersion   string
	ProtocolVersion string
}

type Client struct {
	httpClient      *http.Client
	timeout         time.Duration
	clientVersion   string
	protocolVersion string
}

func New(config Config) *Client {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	protocolVersion := strings.TrimSpace(config.ProtocolVersion)
	if protocolVersion == "" {
		protocolVersion = DefaultProtocolVersion
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clone := *httpClient
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		httpClient: &clone, timeout: timeout,
		clientVersion: strings.TrimSpace(config.ClientVersion), protocolVersion: protocolVersion,
	}
}

func (client *Client) Discover(ctx context.Context, serviceAddress string) (Document, error) {
	baseURL, err := profile.NormalizeBaseURL(serviceAddress)
	if err != nil {
		return Document{}, err
	}
	requestContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, baseURL+Path, nil)
	if err != nil {
		return Document{}, errors.New("create discovery request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return Document{}, errors.New("service discovery timed out")
		}
		return Document{}, fmt.Errorf("service discovery failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return Document{}, errors.New("service discovery redirects are not allowed")
	}
	if response.StatusCode != http.StatusOK {
		return Document{}, fmt.Errorf("service discovery returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxDocumentBytes+1))
	if err != nil {
		return Document{}, errors.New("read service discovery response")
	}
	if len(raw) > MaxDocumentBytes {
		return Document{}, errors.New("service discovery response exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, errors.New("service discovery response contains invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Document{}, errors.New("service discovery response must contain one JSON document")
	}
	if err := client.validate(baseURL, document); err != nil {
		return Document{}, err
	}
	document.PublicURL, _ = profile.NormalizeBaseURL(document.PublicURL)
	document.TunnelPath, _ = NormalizeTunnelPath(document.TunnelPath)
	return document, nil
}

func (client *Client) validate(baseURL string, document Document) error {
	if !validIdentifier(document.ServiceID) {
		return errors.New("service discovery returned an invalid service ID")
	}
	publicURL, err := profile.NormalizeBaseURL(document.PublicURL)
	if err != nil {
		return errors.New("service discovery returned an invalid public URL")
	}
	if !sameOrigin(baseURL, publicURL) {
		return errors.New("service discovery public URL does not match the configured origin")
	}
	if _, err := NormalizeTunnelPath(document.TunnelPath); err != nil {
		return fmt.Errorf("service discovery returned an invalid tunnel path: %w", err)
	}
	if !slices.Contains(document.APIVersions, "v2") {
		return errors.New("service does not support API v2")
	}
	if err := validateProtocol(client.protocolVersion, document.ProtocolMin, document.ProtocolMax); err != nil {
		return err
	}
	if err := validateClientVersion(client.clientVersion, document.MinClientVersion); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(document.AuthMethods))
	for _, method := range document.AuthMethods {
		method.ID = strings.TrimSpace(method.ID)
		if !validIdentifier(method.ID) {
			return errors.New("service discovery returned an invalid authentication method ID")
		}
		if _, exists := seen[method.ID]; exists {
			return errors.New("service discovery returned duplicate authentication method IDs")
		}
		seen[method.ID] = struct{}{}
		switch {
		case method.Type == "oidc" && method.Interaction == "browser":
		case method.Type == "ad" && method.Interaction == "password":
		case method.Type == "static-token" && method.Interaction == "token":
		case method.Type == "anonymous" && method.Interaction == "none":
		default:
			return fmt.Errorf("service discovery returned unsupported authentication method %q", method.ID)
		}
	}
	return nil
}

func NormalizeTunnelPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = DefaultTunnelPath
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !strings.HasPrefix(value, "/") || parsed.IsAbs() || parsed.Host != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.EscapedPath() != value ||
		strings.Contains(value, "//") || strings.Contains(value, "/./") || strings.Contains(value, "/../") ||
		strings.HasSuffix(value, "/.") || strings.HasSuffix(value, "/..") {
		return "", errors.New("must be a clean absolute URL path without escaping, query parameters or a fragment")
	}
	return value, nil
}

func validateProtocol(current, minimum, maximum string) error {
	currentVersion, currentErr := version.NewVersion(strings.TrimSpace(current))
	minimumVersion, minimumErr := version.NewVersion(strings.TrimSpace(minimum))
	maximumVersion, maximumErr := version.NewVersion(strings.TrimSpace(maximum))
	if currentErr != nil || minimumErr != nil || maximumErr != nil || minimumVersion.GreaterThan(maximumVersion) {
		return errors.New("service discovery returned an invalid protocol range")
	}
	if currentVersion.LessThan(minimumVersion) || currentVersion.GreaterThan(maximumVersion) {
		message := fmt.Sprintf("service protocol range %s-%s is incompatible with client protocol %s", minimum, maximum, current)
		return &CompatibilityError{
			Code: CodeVersionMismatch, Message: message, ProtocolVersion: current,
			Minimum: minimum, Maximum: maximum,
		}
	}
	return nil
}

func validateClientVersion(current, minimum string) error {
	current = strings.TrimSpace(current)
	minimum = strings.TrimSpace(minimum)
	if minimum == "" || current == "" || current == "dev" {
		return nil
	}
	currentVersion, currentErr := version.NewVersion(strings.TrimPrefix(current, "v"))
	minimumVersion, minimumErr := version.NewVersion(strings.TrimPrefix(minimum, "v"))
	if minimumErr != nil {
		return errors.New("service discovery returned an invalid minimum client version")
	}
	if currentErr != nil {
		return errors.New("client version is invalid")
	}
	if currentVersion.LessThan(minimumVersion) {
		return &CompatibilityError{
			Code:          CodeClientVersionUnsupported,
			Message:       fmt.Sprintf("service requires client version %s or newer", minimum),
			ClientVersion: current, Minimum: minimum,
		}
	}
	return nil
}

func sameOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Hostname(), rightURL.Hostname()) &&
		effectivePort(leftURL) == effectivePort(rightURL)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if value.Scheme == "https" {
		return "443"
	}
	return "80"
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._", character) {
			continue
		}
		return false
	}
	return true
}
