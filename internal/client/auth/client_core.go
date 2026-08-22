package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/authconfig"
)

const (
	DefaultRequestTimeout = 15 * time.Second
	DefaultLoginTimeout   = 5 * time.Minute
	maxResponseBytes      = 64 << 10

	CodeInvalidRequest         = "invalid_request"
	CodeInvalidClient          = "invalid_client"
	CodeInvalidGrant           = "invalid_grant"
	CodeUnsupportedGrantType   = "unsupported_grant_type"
	CodeInvalidToken           = "invalid_token"
	CodeTemporarilyUnavailable = "temporarily_unavailable"
)

var ErrLoginExpired = errors.New("gateway login expired; sign in again")

type BrowserOpener func(string) error

type Config struct {
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	LoginTimeout     time.Duration
	OpenBrowser      BrowserOpener
	BrowserCallback  func()
	ClientID         string
	RedirectURI      string
	LoopbackCallback bool
}

type Client struct {
	httpClient       *http.Client
	requestTimeout   time.Duration
	loginTimeout     time.Duration
	openBrowser      BrowserOpener
	browserCallback  func()
	clientID         string
	redirectURI      string
	loopbackCallback bool
	callbackMu       sync.Mutex
	pendingCallback  *pendingCallback
}

type tokenResponse struct {
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}

type errorResponse struct {
	Code    string `json:"error"`
	Message string `json:"error_description"`
}

type providerMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

// APIError is a typed authentication endpoint rejection. Callers can branch
// on Status or Code without parsing server-controlled human-readable text.
type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (apiError *APIError) Error() string {
	if apiError == nil {
		return ""
	}
	if apiError.Code != "" {
		return fmt.Sprintf("authentication failed (%s): %s", apiError.Code, apiError.Message)
	}
	return fmt.Sprintf("authentication request returned HTTP %d", apiError.Status)
}

// IsInvalidGrant reports an unrecoverable OAuth grant rejection. Callers must
// discard the local credential and require a new browser login rather than
// retrying a refresh token that the server has expired or revoked.
func IsInvalidGrant(err error) bool {
	var apiError *APIError
	return errors.As(err, &apiError) && apiError.Code == CodeInvalidGrant
}

type callbackResult struct {
	code string
	err  error
}

type pendingCallback struct {
	state     string
	result    chan callbackResult
	delivered bool
}

func New(config Config) *Client {
	requestTimeout := config.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = DefaultRequestTimeout
	}
	loginTimeout := config.LoginTimeout
	if loginTimeout <= 0 {
		loginTimeout = DefaultLoginTimeout
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clone := *httpClient
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	clientID := strings.TrimSpace(config.ClientID)
	if clientID == "" {
		clientID = DefaultClientID
	}
	redirectURI := strings.TrimSpace(config.RedirectURI)
	if redirectURI == "" {
		redirectURI = DefaultRedirectURI
	}
	return &Client{
		httpClient: &clone, requestTimeout: requestTimeout, loginTimeout: loginTimeout,
		openBrowser: config.OpenBrowser, browserCallback: config.BrowserCallback,
		clientID: clientID, redirectURI: redirectURI, loopbackCallback: config.LoopbackCallback,
	}
}

const (
	DefaultClientID    = authconfig.DesktopClientID
	DefaultRedirectURI = authconfig.DesktopRedirectURI
)
