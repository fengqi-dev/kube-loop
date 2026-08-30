package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

const (
	Path                         = "/.well-known/kubeloop"
	DefaultProtocolVersion       = "2.0"
	DefaultTunnelPath            = "/tunnel"
	DefaultTimeout               = 10 * time.Second
	MaxDocumentBytes             = 64 << 10
	CodeVersionMismatch          = "VERSION_MISMATCH"
	CodeClientVersionUnsupported = "CLIENT_VERSION_UNSUPPORTED"
	discoveryAttempts            = 3
)

var discoveryRetryDelays = [...]time.Duration{100 * time.Millisecond, 250 * time.Millisecond}

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

func (client *Client) Discover(ctx context.Context, serviceAddress string) (_ Document, resultErr error) {
	baseURL, err := profile.NormalizeBaseURL(serviceAddress)
	if err != nil {
		return Document{}, err
	}
	requestContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	var response *http.Response
	var requestErr error
	for attempt := range discoveryAttempts {
		request, err := http.NewRequestWithContext(requestContext, http.MethodGet, baseURL+Path, nil)
		if err != nil {
			return Document{}, errors.New("create discovery request")
		}
		request.Header.Set("Accept", "application/json")
		response, requestErr = client.httpClient.Do(request)
		if requestErr == nil {
			break
		}
		if requestContext.Err() != nil {
			break
		}
		if attempt < len(discoveryRetryDelays) {
			timer := time.NewTimer(discoveryRetryDelays[attempt])
			select {
			case <-requestContext.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	if requestErr != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return Document{}, errors.New("service discovery timed out")
		}
		return Document{}, fmt.Errorf("service discovery failed: %w", requestErr)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close service discovery response: %w", err))
		}
	}()
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
	// The explicitly configured scheme controls transport security. Discovery
	// may advertise the same service through another scheme, but must not turn
	// an HTTP selection into HTTPS (or vice versa).
	document.PublicURL = baseURL
	document.TunnelPath, _ = NormalizeTunnelPath(document.TunnelPath)
	return document, nil
}
