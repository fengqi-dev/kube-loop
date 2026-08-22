package remote

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
)

const (
	DefaultRequestTimeout = 30 * time.Second
	defaultRefreshAhead   = 30 * time.Second
	defaultCapabilityTTL  = 30 * time.Second
	defaultCapabilitySize = 128
	maximumResponseBytes  = 2 << 20
	pageLimit             = 500
	maximumPages          = 20

	CodeUnauthenticated = "UNAUTHENTICATED"
	CodeForbidden       = "FORBIDDEN"
	CodeNotFound        = "NOT_FOUND"
	CodeConflict        = "CONFLICT"
	CodeInvalidArgument = "INVALID_ARGUMENT"
	CodeUnavailable     = "UNAVAILABLE"
	CodeVersionMismatch = "VERSION_MISMATCH"
	CodeRateLimited     = "RATE_LIMITED"
	CodeInternal        = "INTERNAL"
)

type TokenRefresher interface {
	Refresh(context.Context, string, credentials.Credential) (credentials.Credential, error)
}

type Config struct {
	HTTPClient             *http.Client
	RequestTimeout         time.Duration
	RefreshAhead           time.Duration
	CapabilityCacheTTL     time.Duration
	CapabilityCacheEntries int
	Now                    func() time.Time
}

type Client struct {
	credentials     credentials.Store
	refresher       TokenRefresher
	httpClient      *http.Client
	requestTimeout  time.Duration
	refreshAhead    time.Duration
	now             func() time.Time
	refreshMu       sync.Mutex
	capabilityMu    sync.Mutex
	capabilityTTL   time.Duration
	capabilitySize  int
	capabilityBind  map[capabilityAuthScope]capabilityBinding
	capabilityCache map[capabilityCacheKey]capabilityCacheEntry
}

func New(credentialStore credentials.Store, refresher TokenRefresher, config Config) (*Client, error) {
	if credentialStore == nil || refresher == nil {
		return nil, errors.New("remote Gateway credentials and token refresher are required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clone := *httpClient
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = DefaultRequestTimeout
	}
	if config.RefreshAhead <= 0 {
		config.RefreshAhead = defaultRefreshAhead
	}
	if config.CapabilityCacheTTL <= 0 {
		config.CapabilityCacheTTL = defaultCapabilityTTL
	}
	if config.CapabilityCacheEntries <= 0 {
		config.CapabilityCacheEntries = defaultCapabilitySize
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Client{
		credentials: credentialStore, refresher: refresher, httpClient: &clone,
		requestTimeout: config.RequestTimeout, refreshAhead: config.RefreshAhead, now: config.Now,
		capabilityTTL: config.CapabilityCacheTTL, capabilitySize: config.CapabilityCacheEntries,
		capabilityBind:  make(map[capabilityAuthScope]capabilityBinding),
		capabilityCache: make(map[capabilityCacheKey]capabilityCacheEntry),
	}, nil
}
