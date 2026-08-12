package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/google/uuid"
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

type APIError struct {
	Status    int
	Code      string
	Message   string
	Field     string
	RequestID string
}

func (apiError *APIError) Error() string {
	if apiError == nil {
		return ""
	}
	if apiError.Code != "" {
		return fmt.Sprintf("Gateway request failed (%s): %s", apiError.Code, apiError.Message)
	}
	return fmt.Sprintf("Gateway request returned HTTP %d", apiError.Status)
}

type Version struct {
	GitVersion     string `json:"gitVersion"`
	GatewayVersion string `json:"gatewayVersion"`
}

type Capabilities = capability.Snapshot

type capabilityAuthScope struct {
	ProfileID  string
	BaseURL    string
	Credential [sha256.Size]byte
}

type capabilityBinding struct {
	PrincipalID    string
	GatewayVersion string
}

type capabilityCacheKey struct {
	Scope          capabilityAuthScope
	PrincipalID    string
	Namespace      string
	GatewayVersion string
}

type capabilityCacheEntry struct {
	Value     Capabilities
	CachedAt  time.Time
	ExpiresAt time.Time
}

type Namespace struct {
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

type Pod struct {
	Name       string    `json:"name"`
	Namespace  string    `json:"namespace"`
	Phase      string    `json:"phase,omitempty"`
	PodIP      string    `json:"podIp,omitempty"`
	NodeName   string    `json:"nodeName,omitempty"`
	Ready      bool      `json:"ready"`
	Containers []string  `json:"containers"`
	Ports      []PodPort `json:"ports"`
}

type PodPort struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

type Service struct {
	Name         string        `json:"name"`
	Namespace    string        `json:"namespace"`
	Type         string        `json:"type"`
	ClusterIP    string        `json:"clusterIp,omitempty"`
	ExternalName string        `json:"externalName,omitempty"`
	Ports        []ServicePort `json:"ports"`
}

type ServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	Protocol   string `json:"protocol"`
	TargetPort string `json:"targetPort,omitempty"`
}

type Session struct {
	ID              string           `json:"id"`
	Namespace       string           `json:"namespace"`
	State           string           `json:"state"`
	Generation      uint64           `json:"generation"`
	CreatedAt       time.Time        `json:"createdAt"`
	UpdatedAt       time.Time        `json:"updatedAt"`
	LastHeartbeatAt time.Time        `json:"lastHeartbeatAt"`
	ExpiresAt       time.Time        `json:"expiresAt"`
	NetworkSpec     networkspec.Spec `json:"networkSpec"`
	NetworkSpecHash string           `json:"networkSpecHash"`
	Capabilities    *Capabilities    `json:"capabilities,omitempty"`
}

type RelayTicket struct {
	TokenType string    `json:"tokenType"`
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expiresAt"`
	DeviceID  string    `json:"deviceId"`
	RelayID   string    `json:"relayId,omitempty"`
	Endpoint  string    `json:"endpoint,omitempty"`
}

type PortForwardSpec struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	RemotePort uint16 `json:"remotePort"`
}

type PortForwardTask struct {
	ID          string           `json:"id"`
	SessionID   string           `json:"sessionId"`
	Namespace   string           `json:"namespace"`
	State       remotetask.State `json:"state"`
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	Protocol    string           `json:"protocol"`
	RemotePort  uint16           `json:"remotePort"`
	DialAddress string           `json:"dialAddress"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	ExpiresAt   time.Time        `json:"expiresAt"`
}

type ExchangePort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type ExchangeSpec struct {
	Service string         `json:"service"`
	Ports   []ExchangePort `json:"ports"`
}

type ExchangeTask struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Service   string           `json:"service"`
	ClusterIP string           `json:"clusterIp"`
	Ports     []ExchangePort   `json:"ports"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	ExpiresAt time.Time        `json:"expiresAt"`
}

type MirrorPort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type MirrorSpec struct {
	Service string       `json:"service"`
	Ports   []MirrorPort `json:"ports"`
}

type MirrorTask struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Service   string           `json:"service"`
	ClusterIP string           `json:"clusterIp"`
	Ports     []MirrorPort     `json:"ports"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	ExpiresAt time.Time        `json:"expiresAt"`
}

type PreviewPort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type PreviewSpec struct {
	Name  string        `json:"name"`
	Ports []PreviewPort `json:"ports"`
}

type PreviewTask struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Name      string           `json:"name"`
	ClusterIP string           `json:"clusterIp,omitempty"`
	Ports     []PreviewPort    `json:"ports"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

type ExecSpec struct {
	Pod       string   `json:"pod"`
	Container string   `json:"container,omitempty"`
	Command   []string `json:"command"`
	TTY       bool     `json:"tty"`
}

type ExecTask struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Pod       string           `json:"pod"`
	Container string           `json:"container,omitempty"`
	TTY       bool             `json:"tty"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	ExpiresAt time.Time        `json:"expiresAt"`
}

type FileTransferSpec struct {
	Direction  string `json:"direction"`
	Kind       string `json:"kind"`
	Pod        string `json:"pod"`
	Container  string `json:"container,omitempty"`
	RemotePath string `json:"remotePath"`
	Size       uint64 `json:"size,omitempty"`
	Offset     uint64 `json:"offset,omitempty"`
	Checksum   string `json:"checksum,omitempty"`
	Overwrite  bool   `json:"overwrite,omitempty"`
	ResumeID   string `json:"resumeId,omitempty"`
}

type FileTransferTask struct {
	ID         string           `json:"id"`
	SessionID  string           `json:"sessionId"`
	Namespace  string           `json:"namespace"`
	State      remotetask.State `json:"state"`
	Direction  string           `json:"direction"`
	Kind       string           `json:"kind"`
	Pod        string           `json:"pod"`
	Container  string           `json:"container"`
	RemotePath string           `json:"remotePath"`
	Size       uint64           `json:"size,omitempty"`
	Offset     uint64           `json:"offset,omitempty"`
	Checksum   string           `json:"checksum,omitempty"`
	Overwrite  bool             `json:"overwrite,omitempty"`
	ResumeID   string           `json:"resumeId,omitempty"`
	CreatedAt  time.Time        `json:"createdAt"`
	UpdatedAt  time.Time        `json:"updatedAt"`
	ExpiresAt  time.Time        `json:"expiresAt"`
}

type PodFileSpec struct {
	Pod         string `json:"pod"`
	Container   string `json:"container,omitempty"`
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Recursive   bool   `json:"recursive,omitempty"`
}

type PodFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

type PodFileList struct {
	SessionID string         `json:"sessionId"`
	Namespace string         `json:"namespace"`
	Pod       string         `json:"pod"`
	Container string         `json:"container"`
	Path      string         `json:"path"`
	Items     []PodFileEntry `json:"items"`
}

type PodFileResult struct {
	Completed bool   `json:"completed"`
	Error     string `json:"error,omitempty"`
}

type PodFileTask struct {
	ID          string           `json:"id"`
	SessionID   string           `json:"sessionId"`
	Namespace   string           `json:"namespace"`
	State       remotetask.State `json:"state"`
	Action      string           `json:"action"`
	Pod         string           `json:"pod"`
	Container   string           `json:"container"`
	Path        string           `json:"path"`
	Destination string           `json:"destination,omitempty"`
	Kind        string           `json:"kind,omitempty"`
	Recursive   bool             `json:"recursive,omitempty"`
	Result      PodFileResult    `json:"result"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	ExpiresAt   time.Time        `json:"expiresAt"`
}

type page[T any] struct {
	Items           []T    `json:"items"`
	Continue        string `json:"continue,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
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

func (client *Client) Version(ctx context.Context, serverProfile profile.Profile) (Version, error) {
	var result Version
	if err := client.getJSON(ctx, serverProfile, "/api/v2/version", nil, &result); err != nil {
		return Version{}, err
	}
	if strings.TrimSpace(result.GitVersion) == "" || strings.TrimSpace(result.GatewayVersion) == "" {
		return Version{}, errors.New("Gateway returned an incomplete version document")
	}
	if scope, scopeErr := client.capabilityAuthScope(serverProfile); scopeErr == nil {
		client.bindGatewayVersion(scope, result.GatewayVersion)
	}
	return result, nil
}

func (client *Client) Capabilities(ctx context.Context, serverProfile profile.Profile, namespace string) (Capabilities, error) {
	if err := validateNamespace(namespace); err != nil {
		return Capabilities{}, err
	}
	scope, err := client.capabilityAuthScope(serverProfile)
	if err != nil {
		return Capabilities{}, err
	}
	if cached, ok := client.cachedCapabilities(scope, namespace); ok {
		return cached, nil
	}
	var result Capabilities
	if err := client.getJSON(ctx, serverProfile, "/api/v2/capabilities", url.Values{"namespace": {namespace}}, &result); err != nil {
		return Capabilities{}, err
	}
	result, err = capability.Normalize(result)
	if err != nil || result.Namespace != namespace {
		return Capabilities{}, errors.New("Gateway returned an invalid capability binding")
	}
	// The request may have rotated credentials. Bind the response only to the
	// authentication session that actually received it.
	scope, err = client.capabilityAuthScope(serverProfile)
	if err != nil {
		return Capabilities{}, err
	}
	client.storeCapabilities(scope, result)
	return cloneCapabilities(result), nil
}

func (client *Client) capabilityAuthScope(serverProfile profile.Profile) (capabilityAuthScope, error) {
	credential, err := client.credentials.Get(serverProfile.ID)
	if err != nil {
		return capabilityAuthScope{}, err
	}
	return capabilityAuthScope{
		ProfileID:  serverProfile.ID,
		BaseURL:    serverProfile.BaseURL,
		Credential: sha256.Sum256([]byte(credential.DeviceID + "\x00" + credential.RefreshToken)),
	}, nil
}

func (client *Client) bindGatewayVersion(scope capabilityAuthScope, gatewayVersion string) {
	client.capabilityMu.Lock()
	defer client.capabilityMu.Unlock()
	binding := client.capabilityBind[scope]
	if binding.GatewayVersion != "" && binding.GatewayVersion != gatewayVersion {
		client.evictCapabilityScopeLocked(scope)
	}
	binding.GatewayVersion = gatewayVersion
	client.capabilityBind[scope] = binding
}

func (client *Client) cachedCapabilities(scope capabilityAuthScope, namespace string) (Capabilities, bool) {
	client.capabilityMu.Lock()
	defer client.capabilityMu.Unlock()
	binding, ok := client.capabilityBind[scope]
	if !ok || binding.PrincipalID == "" || binding.GatewayVersion == "" {
		return Capabilities{}, false
	}
	key := capabilityCacheKey{
		Scope: scope, PrincipalID: binding.PrincipalID, Namespace: namespace, GatewayVersion: binding.GatewayVersion,
	}
	entry, ok := client.capabilityCache[key]
	if !ok {
		return Capabilities{}, false
	}
	if !entry.ExpiresAt.After(client.now()) {
		delete(client.capabilityCache, key)
		return Capabilities{}, false
	}
	return cloneCapabilities(entry.Value), true
}

func (client *Client) storeCapabilities(scope capabilityAuthScope, value Capabilities) {
	now := client.now()
	client.capabilityMu.Lock()
	defer client.capabilityMu.Unlock()
	binding := capabilityBinding{PrincipalID: value.PrincipalID, GatewayVersion: value.GatewayVersion}
	if current, ok := client.capabilityBind[scope]; ok && current != binding {
		client.evictCapabilityScopeLocked(scope)
	}
	client.capabilityBind[scope] = binding
	for key, entry := range client.capabilityCache {
		if !entry.ExpiresAt.After(now) {
			delete(client.capabilityCache, key)
		}
	}
	for len(client.capabilityCache) >= client.capabilitySize {
		var oldestKey capabilityCacheKey
		var oldestTime time.Time
		for key, entry := range client.capabilityCache {
			if oldestTime.IsZero() || entry.CachedAt.Before(oldestTime) {
				oldestKey, oldestTime = key, entry.CachedAt
			}
		}
		delete(client.capabilityCache, oldestKey)
	}
	key := capabilityCacheKey{
		Scope: scope, PrincipalID: value.PrincipalID, Namespace: value.Namespace, GatewayVersion: value.GatewayVersion,
	}
	client.capabilityCache[key] = capabilityCacheEntry{
		Value: cloneCapabilities(value), CachedAt: now, ExpiresAt: now.Add(client.capabilityTTL),
	}
}

func (client *Client) evictCapabilityScopeLocked(scope capabilityAuthScope) {
	for key := range client.capabilityCache {
		if key.Scope == scope {
			delete(client.capabilityCache, key)
		}
	}
}

func cloneCapabilities(value Capabilities) Capabilities {
	value.Capabilities = append([]string(nil), value.Capabilities...)
	return value
}

func cloneCapabilityPointer(value *Capabilities) *Capabilities {
	if value == nil {
		return nil
	}
	cloned := cloneCapabilities(*value)
	return &cloned
}

func (client *Client) Namespaces(ctx context.Context, serverProfile profile.Profile) ([]Namespace, error) {
	return collectPages[Namespace](ctx, client, serverProfile, "/api/v2/namespaces")
}

func (client *Client) Pods(ctx context.Context, serverProfile profile.Profile, namespace string) ([]Pod, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	pods, err := collectPages[Pod](ctx, client, serverProfile, "/api/v2/namespaces/"+url.PathEscape(namespace)+"/pods")
	if err != nil {
		return nil, err
	}
	for index := range pods {
		if pods[index].Containers == nil {
			pods[index].Containers = []string{}
		}
		if pods[index].Ports == nil {
			pods[index].Ports = []PodPort{}
		}
	}
	return pods, nil
}

func (client *Client) Services(ctx context.Context, serverProfile profile.Profile, namespace string) ([]Service, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	return collectPages[Service](ctx, client, serverProfile, "/api/v2/namespaces/"+url.PathEscape(namespace)+"/services")
}

func (client *Client) CreateSession(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace, idempotencyKey string,
) (Session, error) {
	if err := validateNamespace(namespace); err != nil {
		return Session{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return Session{}, errors.New("Session idempotency key is required")
	}
	var result Session
	if err := client.doJSON(
		ctx, serverProfile, http.MethodPost, "/api/v2/sessions", url.Values{"namespace": {namespace}},
		http.Header{"Idempotency-Key": {idempotencyKey}}, &result,
	); err != nil {
		return Session{}, err
	}
	result, err := validateSession(result, namespace)
	if err != nil {
		return Session{}, err
	}
	if result.Capabilities == nil {
		return Session{}, errors.New("Gateway returned a Session without capabilities")
	}
	snapshot, err := capability.Normalize(*result.Capabilities)
	if err != nil || snapshot.Namespace != namespace {
		return Session{}, errors.New("Gateway returned an invalid Session capability binding")
	}
	result.Capabilities = &snapshot
	// Session creation may rotate credentials. Cache the snapshot only under the
	// authentication scope that received the successful response.
	if scope, scopeErr := client.capabilityAuthScope(serverProfile); scopeErr == nil {
		client.storeCapabilities(scope, snapshot)
	}
	return result, nil
}

func (client *Client) GetSession(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace, sessionID string,
) (Session, error) {
	if err := validateSessionTarget(namespace, sessionID); err != nil {
		return Session{}, err
	}
	var result Session
	if err := client.doJSON(
		ctx, serverProfile, http.MethodGet, "/api/v2/sessions/"+url.PathEscape(sessionID),
		url.Values{"namespace": {namespace}}, nil, &result,
	); err != nil {
		return Session{}, err
	}
	return validateSession(result, namespace)
}

func (client *Client) HeartbeatSession(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) (Session, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.Generation == 0 {
		return Session{}, errors.New("current Session identity and generation are required")
	}
	var result Session
	if err := client.doJSON(
		ctx, serverProfile, http.MethodPost, "/api/v2/sessions/"+url.PathEscape(current.ID)+"/heartbeat",
		url.Values{"namespace": {current.Namespace}}, generationHeader(current.Generation), &result,
	); err != nil {
		return Session{}, err
	}
	result, err := validateSession(result, current.Namespace)
	if err != nil {
		return Session{}, err
	}
	result.Capabilities = cloneCapabilityPointer(current.Capabilities)
	return result, nil
}

func (client *Client) DisconnectSession(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) (Session, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.Generation == 0 {
		return Session{}, errors.New("current Session identity and generation are required")
	}
	var result Session
	if err := client.doJSON(
		ctx, serverProfile, http.MethodDelete, "/api/v2/sessions/"+url.PathEscape(current.ID),
		url.Values{"namespace": {current.Namespace}}, generationHeader(current.Generation), &result,
	); err != nil {
		return Session{}, err
	}
	result, err := validateSession(result, current.Namespace)
	if err != nil {
		return Session{}, err
	}
	result.Capabilities = cloneCapabilityPointer(current.Capabilities)
	return result, nil
}

func (client *Client) IssueRelayTicket(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) (RelayTicket, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return RelayTicket{}, errors.New("active Session identity is required")
	}
	body := []byte(`{}`)
	var result RelayTicket
	if err := client.doJSONBody(
		ctx, serverProfile, http.MethodPost,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/tickets",
		url.Values{"namespace": {current.Namespace}}, nil, body, &result,
	); err != nil {
		return RelayTicket{}, err
	}
	if result.TokenType != relayticket.Type || result.Ticket == "" || len(result.Ticket) > relayticket.MaximumTicketBytes ||
		strings.TrimSpace(result.Ticket) != result.Ticket || !result.ExpiresAt.After(client.now()) ||
		!validRelayDeviceID(result.DeviceID) ||
		!validRelayAssignment(result.RelayID, result.Endpoint) {
		return RelayTicket{}, errors.New("Gateway returned an invalid RelayTicket")
	}
	return result, nil
}

func validRelayDeviceID(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
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

func validRelayAssignment(relayID, endpoint string) bool {
	if relayID == "" && endpoint == "" {
		return true
	}
	if !strings.HasPrefix(relayID, "relay-") || len(relayID) != len("relay-")+64 {
		return false
	}
	for _, character := range relayID[len("relay-"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	parsed, err := url.Parse(endpoint)
	return err == nil && parsed.Scheme == "wss" && parsed.Host != "" && parsed.User == nil &&
		parsed.Path != "" && parsed.RawQuery == "" && parsed.Fragment == "" &&
		strings.TrimRight(parsed.Path, "/") == parsed.Path
}

func (client *Client) CreatePortForward(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec PortForwardSpec,
	idempotencyKey string,
) (PortForwardTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return PortForwardTask{}, errors.New("active Session identity is required")
	}
	if err := validatePortForwardSpec(&spec); err != nil {
		return PortForwardTask{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return PortForwardTask{}, errors.New("Port Forward idempotency key is required")
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return PortForwardTask{}, errors.New("encode Port Forward request")
	}
	var result PortForwardTask
	if err := client.doJSONBody(
		ctx, serverProfile, http.MethodPost,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/port-forwards",
		url.Values{"namespace": {current.Namespace}},
		http.Header{"Idempotency-Key": {idempotencyKey}}, body, &result,
	); err != nil {
		return PortForwardTask{}, err
	}
	return validatePortForwardTask(result, current)
}

func (client *Client) ListPortForwards(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) ([]PortForwardTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return nil, errors.New("active Session identity is required")
	}
	var result struct {
		Items []PortForwardTask `json:"items"`
	}
	if err := client.doJSON(
		ctx, serverProfile, http.MethodGet,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/port-forwards",
		url.Values{"namespace": {current.Namespace}}, nil, &result,
	); err != nil {
		return nil, err
	}
	if result.Items == nil {
		result.Items = []PortForwardTask{}
	}
	for index := range result.Items {
		validated, err := validatePortForwardTask(result.Items[index], current)
		if err != nil {
			return nil, err
		}
		result.Items[index] = validated
	}
	return result.Items, nil
}

func (client *Client) StopPortForward(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (PortForwardTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return PortForwardTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return PortForwardTask{}, errors.New("Port Forward Task ID is invalid")
	}
	var result PortForwardTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodDelete,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/port-forwards/"+url.PathEscape(taskID),
		url.Values{"namespace": {current.Namespace}}, nil, &result,
	); err != nil {
		return PortForwardTask{}, err
	}
	return validatePortForwardTask(result, current)
}

func (client *Client) CreateExchange(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec ExchangeSpec,
	idempotencyKey string,
) (ExchangeTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return ExchangeTask{}, errors.New("active Session identity is required")
	}
	if err := validateExchangeSpec(&spec); err != nil {
		return ExchangeTask{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		return ExchangeTask{}, errors.New("Exchange idempotency key is invalid")
	}
	body, _ := json.Marshal(spec)
	var result ExchangeTask
	if err := client.doJSONBody(
		ctx, serverProfile, http.MethodPost,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/exchanges",
		url.Values{"namespace": {current.Namespace}},
		http.Header{"Idempotency-Key": {idempotencyKey}}, body, &result,
	); err != nil {
		return ExchangeTask{}, err
	}
	return validateExchangeTask(result, current)
}

func (client *Client) GetExchange(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (ExchangeTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return ExchangeTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return ExchangeTask{}, errors.New("Exchange Task ID is invalid")
	}
	var result ExchangeTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodGet,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/exchanges/"+url.PathEscape(taskID),
		url.Values{"namespace": {current.Namespace}}, nil, &result,
	); err != nil {
		return ExchangeTask{}, err
	}
	return validateExchangeTask(result, current)
}

func (client *Client) StopExchange(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (ExchangeTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return ExchangeTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return ExchangeTask{}, errors.New("Exchange Task ID is invalid")
	}
	var result ExchangeTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodDelete,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/exchanges/"+url.PathEscape(taskID),
		url.Values{"namespace": {current.Namespace}}, nil, &result,
	); err != nil {
		return ExchangeTask{}, err
	}
	return validateExchangeTask(result, current)
}

func (client *Client) OpenExchangeStream(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	task ExchangeTask,
) (*websocket.Conn, error) {
	if _, err := validateExchangeTask(task, current); err != nil || task.State != "pending" {
		return nil, errors.New("pending Exchange Task is required")
	}
	connection, err := client.openTrafficWebSocket(ctx, serverProfile, current, trafficcontrol.ModeExchange, task.ID)
	if err == nil {
		connection.SetReadLimit(exchangestream.MaximumData + 14)
	}
	return connection, err
}

func (client *Client) CreateMirror(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec MirrorSpec,
	idempotencyKey string,
) (MirrorTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return MirrorTask{}, errors.New("active Session identity is required")
	}
	if err := validateMirrorSpec(&spec); err != nil {
		return MirrorTask{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		return MirrorTask{}, errors.New("Mirror idempotency key is invalid")
	}
	body, _ := json.Marshal(spec)
	var result MirrorTask
	if err := client.doJSONBody(
		ctx, serverProfile, http.MethodPost,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/mirrors",
		url.Values{"namespace": {current.Namespace}},
		http.Header{"Idempotency-Key": {idempotencyKey}}, body, &result,
	); err != nil {
		return MirrorTask{}, err
	}
	return validateMirrorTask(result, current)
}

func (client *Client) GetMirror(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (MirrorTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return MirrorTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return MirrorTask{}, errors.New("Mirror Task ID is invalid")
	}
	var result MirrorTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodGet,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/mirrors/"+url.PathEscape(taskID),
		url.Values{"namespace": {current.Namespace}}, nil, &result,
	); err != nil {
		return MirrorTask{}, err
	}
	return validateMirrorTask(result, current)
}

func (client *Client) StopMirror(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (MirrorTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return MirrorTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return MirrorTask{}, errors.New("Mirror Task ID is invalid")
	}
	var result MirrorTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodDelete,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/mirrors/"+url.PathEscape(taskID),
		url.Values{"namespace": {current.Namespace}}, nil, &result,
	); err != nil {
		return MirrorTask{}, err
	}
	return validateMirrorTask(result, current)
}

func (client *Client) OpenMirrorStream(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	task MirrorTask,
) (*websocket.Conn, error) {
	if _, err := validateMirrorTask(task, current); err != nil || task.State != "pending" {
		return nil, errors.New("pending Mirror Task is required")
	}
	connection, err := client.openTrafficWebSocket(ctx, serverProfile, current, trafficcontrol.ModeMirror, task.ID)
	if err == nil {
		connection.SetReadLimit(mirrorstream.MaximumData + mirrorstream.HeaderSize)
	}
	return connection, err
}

func (client *Client) CreatePreview(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec PreviewSpec,
	idempotencyKey string,
) (PreviewTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return PreviewTask{}, errors.New("active Session identity is required")
	}
	if err := validatePreviewSpec(&spec); err != nil {
		return PreviewTask{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		return PreviewTask{}, errors.New("Preview idempotency key is invalid")
	}
	body, _ := json.Marshal(spec)
	var result PreviewTask
	if err := client.doJSONBody(
		ctx, serverProfile, http.MethodPost,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/previews",
		url.Values{"namespace": {current.Namespace}},
		http.Header{"Idempotency-Key": {idempotencyKey}}, body, &result,
	); err != nil {
		return PreviewTask{}, err
	}
	return validatePreviewTask(result, current)
}

func (client *Client) GetPreview(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (PreviewTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return PreviewTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return PreviewTask{}, errors.New("Preview Task ID is invalid")
	}
	var result PreviewTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodGet,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/previews/"+url.PathEscape(taskID),
		url.Values{"namespace": {current.Namespace}}, nil, &result,
	); err != nil {
		return PreviewTask{}, err
	}
	return validatePreviewTask(result, current)
}

func (client *Client) StopPreview(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (PreviewTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return PreviewTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return PreviewTask{}, errors.New("Preview Task ID is invalid")
	}
	var result PreviewTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodDelete,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/previews/"+url.PathEscape(taskID),
		url.Values{"namespace": {current.Namespace}}, nil, &result,
	); err != nil {
		return PreviewTask{}, err
	}
	return validatePreviewTask(result, current)
}

func (client *Client) OpenPreviewStream(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	task PreviewTask,
) (*websocket.Conn, error) {
	if _, err := validatePreviewTask(task, current); err != nil || task.State != "pending" {
		return nil, errors.New("pending Preview Task is required")
	}
	connection, err := client.openTrafficWebSocket(ctx, serverProfile, current, trafficcontrol.ModePreview, task.ID)
	if err == nil {
		connection.SetReadLimit(exchangestream.MaximumData + 14)
	}
	return connection, err
}

func (client *Client) CreateExecTask(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec ExecSpec,
	idempotencyKey string,
) (ExecTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return ExecTask{}, errors.New("active Session identity is required")
	}
	if err := validateExecSpec(spec); err != nil {
		return ExecTask{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return ExecTask{}, errors.New("Pod exec idempotency key is required")
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return ExecTask{}, errors.New("encode Pod exec request")
	}
	var result ExecTask
	if err := client.doJSONBody(
		ctx, serverProfile, http.MethodPost,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/exec",
		url.Values{"namespace": {current.Namespace}},
		http.Header{"Idempotency-Key": {idempotencyKey}}, body, &result,
	); err != nil {
		return ExecTask{}, err
	}
	return validateExecTask(result, current)
}

func (client *Client) OpenExecStream(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	task ExecTask,
) (*websocket.Conn, error) {
	if _, err := validateExecTask(task, current); err != nil || task.State != "pending" {
		return nil, errors.New("pending Pod exec Task is required")
	}
	return client.openTaskWebSocket(ctx, serverProfile, current,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/exec/"+url.PathEscape(task.ID)+"/stream")
}

func (client *Client) CreateFileTransferTask(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec FileTransferSpec,
	idempotencyKey string,
) (FileTransferTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return FileTransferTask{}, errors.New("active Session identity is required")
	}
	if err := validateFileTransferSpec(&spec); err != nil {
		return FileTransferTask{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return FileTransferTask{}, errors.New("file transfer idempotency key is required")
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return FileTransferTask{}, errors.New("encode file transfer request")
	}
	var result FileTransferTask
	if err := client.doJSONBody(
		ctx, serverProfile, http.MethodPost,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/file-transfers",
		url.Values{"namespace": {current.Namespace}},
		http.Header{"Idempotency-Key": {idempotencyKey}}, body, &result,
	); err != nil {
		return FileTransferTask{}, err
	}
	return validateFileTransferTask(result, current)
}

func (client *Client) GetFileTransferTask(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (FileTransferTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return FileTransferTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return FileTransferTask{}, errors.New("file transfer Task ID is invalid")
	}
	var result FileTransferTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodGet,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/file-transfers/"+url.PathEscape(taskID),
		url.Values{"namespace": {current.Namespace}}, nil, &result,
	); err != nil {
		return FileTransferTask{}, err
	}
	return validateFileTransferTask(result, current)
}

func (client *Client) OpenFileTransferStream(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	task FileTransferTask,
) (*websocket.Conn, error) {
	if _, err := validateFileTransferTask(task, current); err != nil || task.State != "pending" {
		return nil, errors.New("pending file transfer Task is required")
	}
	connection, err := client.openTaskWebSocket(ctx, serverProfile, current,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/file-transfers/"+url.PathEscape(task.ID)+"/stream")
	if err == nil {
		connection.SetReadLimit(filestream.MaximumData + 1)
	}
	return connection, err
}

func (client *Client) ListPodFiles(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec PodFileSpec,
) (PodFileList, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return PodFileList{}, errors.New("active Session identity is required")
	}
	if err := validatePodFileSpec("list", &spec); err != nil {
		return PodFileList{}, err
	}
	body, _ := json.Marshal(spec)
	var result PodFileList
	if err := client.doJSONBody(ctx, serverProfile, http.MethodPost,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/pod-files/list",
		url.Values{"namespace": {current.Namespace}}, nil, body, &result); err != nil {
		return PodFileList{}, err
	}
	return validatePodFileList(result, current, spec)
}

func (client *Client) CreatePodFileOperation(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	action string,
	spec PodFileSpec,
	idempotencyKey string,
) (PodFileTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return PodFileTask{}, errors.New("active Session identity is required")
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if err := validatePodFileSpec(action, &spec); err != nil {
		return PodFileTask{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		return PodFileTask{}, errors.New("remote file operation idempotency key is invalid")
	}
	body, _ := json.Marshal(spec)
	var result PodFileTask
	if err := client.doJSONBody(ctx, serverProfile, http.MethodPost,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/pod-files/"+action,
		url.Values{"namespace": {current.Namespace}}, http.Header{"Idempotency-Key": {idempotencyKey}}, body, &result); err != nil {
		return PodFileTask{}, err
	}
	return validatePodFileTask(result, current)
}

func (client *Client) GetPodFileOperation(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (PodFileTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != "active" {
		return PodFileTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return PodFileTask{}, errors.New("remote file operation Task ID is invalid")
	}
	var result PodFileTask
	if err := client.doJSON(ctx, serverProfile, http.MethodGet,
		"/api/v2/sessions/"+url.PathEscape(current.ID)+"/pod-files/operations/"+url.PathEscape(taskID),
		url.Values{"namespace": {current.Namespace}}, nil, &result); err != nil {
		return PodFileTask{}, err
	}
	return validatePodFileTask(result, current)
}

func (client *Client) openTaskWebSocket(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	streamPath string,
) (*websocket.Conn, error) {
	baseURL, err := profile.NormalizeBaseURL(serverProfile.BaseURL)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return nil, errors.New("Server Profile URL is invalid")
	}
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	endpoint.Path = streamPath
	endpoint.RawQuery = url.Values{"namespace": {current.Namespace}}.Encode()
	credential, err := client.usableCredential(ctx, serverProfile, "")
	if err != nil {
		return nil, err
	}
	connection, response, err := client.dialWebSocket(ctx, endpoint.String(), credential.AccessToken)
	if err == nil {
		return connection, nil
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		return nil, err
	}
	credential, refreshErr := client.usableCredential(ctx, serverProfile, credential.AccessToken)
	if refreshErr != nil {
		return nil, refreshErr
	}
	connection, _, err = client.dialWebSocket(ctx, endpoint.String(), credential.AccessToken)
	return connection, err
}

func (client *Client) openTrafficWebSocket(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	mode trafficcontrol.Mode,
	taskID string,
) (*websocket.Conn, error) {
	ticket, err := client.IssueRelayTicket(ctx, serverProfile, current)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(ticket.Endpoint)
	if err != nil || endpoint.Scheme != "wss" || endpoint.Host == "" {
		return nil, errors.New("RelayTicket endpoint is invalid")
	}
	endpoint.Path = trafficcontrol.PublicPathPrefix + "/" + url.PathEscape(string(mode)) + "/" + url.PathEscape(taskID)
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	connection, _, err := client.dialWebSocket(ctx, endpoint.String(), ticket.Ticket)
	return connection, err
}

func (client *Client) dialWebSocket(ctx context.Context, endpoint, accessToken string) (*websocket.Conn, *http.Response, error) {
	connection, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPClient:      client.httpClient,
		HTTPHeader:      http.Header{"Authorization": {"Bearer " + accessToken}},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err == nil {
		return connection, response, nil
	}
	if response == nil {
		return nil, nil, fmt.Errorf("Gateway WebSocket stream failed: %w", err)
	}
	defer response.Body.Close()
	contents, _ := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	return nil, response, decodeAPIError(response.StatusCode, contents)
}

func collectPages[T any](ctx context.Context, client *Client, serverProfile profile.Profile, path string) ([]T, error) {
	items := make([]T, 0)
	continueToken := ""
	for range maximumPages {
		query := url.Values{"limit": {strconv.Itoa(pageLimit)}}
		if continueToken != "" {
			query.Set("continue", continueToken)
		}
		var result page[T]
		if err := client.getJSON(ctx, serverProfile, path, query, &result); err != nil {
			return nil, err
		}
		if result.Items == nil {
			result.Items = []T{}
		}
		items = append(items, result.Items...)
		if result.Continue == "" {
			return items, nil
		}
		if result.Continue == continueToken {
			return nil, errors.New("Gateway returned a repeated pagination token")
		}
		continueToken = result.Continue
	}
	return nil, errors.New("Gateway inventory exceeds the pagination safety limit")
}

func (client *Client) getJSON(ctx context.Context, serverProfile profile.Profile, path string, query url.Values, destination any) error {
	return client.doJSON(ctx, serverProfile, http.MethodGet, path, query, nil, destination)
}

func (client *Client) doJSON(
	ctx context.Context,
	serverProfile profile.Profile,
	method, path string,
	query url.Values,
	headers http.Header,
	destination any,
) error {
	return client.doJSONBody(ctx, serverProfile, method, path, query, headers, nil, destination)
}

func (client *Client) doJSONBody(
	ctx context.Context,
	serverProfile profile.Profile,
	method, path string,
	query url.Values,
	headers http.Header,
	body []byte,
	destination any,
) error {
	baseURL, err := profile.NormalizeBaseURL(serverProfile.BaseURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(serverProfile.ID) == "" {
		return errors.New("Server Profile ID is required")
	}
	credential, err := client.usableCredential(ctx, serverProfile, "")
	if err != nil {
		return err
	}
	status, response, err := client.request(ctx, method, baseURL, path, query, headers, body, credential.AccessToken)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		credential, err = client.usableCredential(ctx, serverProfile, credential.AccessToken)
		if err != nil {
			return err
		}
		status, response, err = client.request(ctx, method, baseURL, path, query, headers, body, credential.AccessToken)
		if err != nil {
			return err
		}
	}
	if status < 200 || status >= 300 {
		return decodeAPIError(status, response)
	}
	decoder := json.NewDecoder(bytes.NewReader(response))
	if err := decoder.Decode(destination); err != nil {
		return errors.New("Gateway response contains invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Gateway response must contain one JSON document")
	}
	return nil
}

func (client *Client) usableCredential(ctx context.Context, serverProfile profile.Profile, rejectedAccessToken string) (credentials.Credential, error) {
	current, err := client.credentials.Get(serverProfile.ID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if rejectedAccessToken == "" && current.AccessExpiresAt.After(client.now().Add(client.refreshAhead)) {
		return current, nil
	}
	client.refreshMu.Lock()
	defer client.refreshMu.Unlock()
	current, err = client.credentials.Get(serverProfile.ID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if rejectedAccessToken != "" && current.AccessToken != rejectedAccessToken {
		return current, nil
	}
	if rejectedAccessToken == "" && current.AccessExpiresAt.After(client.now().Add(client.refreshAhead)) {
		return current, nil
	}
	if !current.RefreshExpiresAt.IsZero() && !current.RefreshExpiresAt.After(client.now()) {
		return credentials.Credential{}, errors.New("Gateway login has expired; sign in again")
	}
	refreshed, err := client.refresher.Refresh(ctx, serverProfile.BaseURL, current)
	if err != nil {
		return credentials.Credential{}, fmt.Errorf("refresh Gateway login: %w", err)
	}
	if err := client.credentials.Set(serverProfile.ID, refreshed); err != nil {
		return credentials.Credential{}, fmt.Errorf("store refreshed Gateway login: %w", err)
	}
	return refreshed, nil
}

func (client *Client) request(
	ctx context.Context,
	method, baseURL, path string,
	query url.Values,
	headers http.Header,
	body []byte,
	accessToken string,
) (int, []byte, error) {
	requestContext, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()
	endpoint := baseURL + path
	if len(query) != 0 {
		endpoint += "?" + query.Encode()
	}
	var requestBody io.Reader
	if body != nil {
		requestBody = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(requestContext, method, endpoint, requestBody)
	if err != nil {
		return 0, nil, errors.New("create Gateway request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return 0, nil, errors.New("Gateway request timed out")
		}
		return 0, nil, fmt.Errorf("Gateway request failed: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		return 0, nil, errors.New("read Gateway response")
	}
	if len(contents) > maximumResponseBytes {
		return 0, nil, errors.New("Gateway response exceeds 2 MiB")
	}
	return response.StatusCode, contents, nil
}

func decodeAPIError(status int, contents []byte) error {
	document := struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Field     string `json:"field,omitempty"`
			RequestID string `json:"requestId"`
		} `json:"error"`
	}{}
	if json.Unmarshal(contents, &document) == nil && document.Error.Code != "" {
		return &APIError{
			Status: status, Code: document.Error.Code, Message: document.Error.Message,
			Field: document.Error.Field, RequestID: document.Error.RequestID,
		}
	}
	return &APIError{Status: status}
}

func validateNamespace(namespace string) error {
	if namespace == "" || len(namespace) > 63 || namespace[0] == '-' || namespace[len(namespace)-1] == '-' {
		return errors.New("namespace is invalid")
	}
	for _, character := range namespace {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return errors.New("namespace is invalid")
	}
	return nil
}

func validateSessionTarget(namespace, sessionID string) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if _, err := uuid.Parse(strings.TrimSpace(sessionID)); err != nil {
		return errors.New("Session ID is invalid")
	}
	return nil
}

func validateSession(session Session, namespace string) (Session, error) {
	if err := validateSessionTarget(namespace, session.ID); err != nil || session.Namespace != namespace ||
		session.Generation == 0 || session.State == "" || session.CreatedAt.IsZero() || session.ExpiresAt.IsZero() ||
		!validDigest(session.NetworkSpecHash) {
		return Session{}, errors.New("Gateway returned an incomplete Session")
	}
	normalized, err := networkspec.Normalize(session.NetworkSpec)
	if err != nil {
		return Session{}, errors.New("Gateway returned an invalid NetworkSpec")
	}
	hash, err := networkspec.Hash(normalized)
	if err != nil || hash != session.NetworkSpecHash {
		return Session{}, errors.New("Gateway returned a mismatched NetworkSpec hash")
	}
	session.NetworkSpec = normalized
	return session, nil
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validatePortForwardSpec(spec *PortForwardSpec) error {
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Protocol = strings.ToLower(strings.TrimSpace(spec.Protocol))
	if spec.Kind != "pod" && spec.Kind != "service" {
		return errors.New("Port Forward kind must be pod or service")
	}
	if !validDNSSubdomain(spec.Name) {
		return errors.New("Port Forward target name is invalid")
	}
	if spec.Protocol == "" {
		spec.Protocol = "tcp"
	}
	if spec.Protocol != "tcp" && spec.Protocol != "udp" {
		return errors.New("Port Forward protocol must be tcp or udp")
	}
	if spec.RemotePort == 0 {
		return errors.New("Port Forward remote port is required")
	}
	return nil
}

func validDNSSubdomain(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for label := range strings.SplitSeq(value, ".") {
		if label == "" || len(label) > 63 || !isLowerAlphaNumeric(label[0]) || !isLowerAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func isLowerAlphaNumeric(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
}

func validatePortForwardTask(task PortForwardTask, session Session) (PortForwardTask, error) {
	if _, err := uuid.Parse(task.ID); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() || task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero() {
		return PortForwardTask{}, errors.New("Gateway returned an incomplete Port Forward Task")
	}
	spec := PortForwardSpec{Kind: task.Kind, Name: task.Name, Protocol: task.Protocol, RemotePort: task.RemotePort}
	if err := validatePortForwardSpec(&spec); err != nil {
		return PortForwardTask{}, errors.New("Gateway returned an invalid Port Forward Task")
	}
	host, rawPort, err := net.SplitHostPort(task.DialAddress)
	if err != nil || net.ParseIP(host) == nil {
		return PortForwardTask{}, errors.New("Gateway returned an invalid Port Forward target")
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return PortForwardTask{}, errors.New("Gateway returned an invalid Port Forward target")
	}
	task.Kind, task.Name, task.Protocol = spec.Kind, spec.Name, spec.Protocol
	return task, nil
}

func validateExchangeSpec(spec *ExchangeSpec) error {
	spec.Service = strings.TrimSpace(spec.Service)
	if !validDNSSubdomain(spec.Service) || len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return errors.New("Exchange Service and ports are invalid")
	}
	seen := make(map[string]struct{}, len(spec.Ports))
	for index := range spec.Ports {
		port := &spec.Ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 || (port.Protocol != "tcp" && port.Protocol != "udp") {
			return errors.New("Exchange Service port is invalid")
		}
		key := strconv.Itoa(int(port.ServicePort)) + "/" + port.Protocol
		if _, exists := seen[key]; exists {
			return errors.New("Exchange Service ports must be unique")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateExchangeTask(task ExchangeTask, session Session) (ExchangeTask, error) {
	if _, err := uuid.Parse(task.ID); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() || net.ParseIP(task.ClusterIP) == nil || task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero() {
		return ExchangeTask{}, errors.New("Gateway returned an incomplete Exchange Task")
	}
	spec := ExchangeSpec{Service: task.Service, Ports: append([]ExchangePort(nil), task.Ports...)}
	if err := validateExchangeSpec(&spec); err != nil {
		return ExchangeTask{}, errors.New("Gateway returned an invalid Exchange Task")
	}
	task.Service, task.Ports = spec.Service, spec.Ports
	return task, nil
}

func validateMirrorSpec(spec *MirrorSpec) error {
	spec.Service = strings.TrimSpace(spec.Service)
	if !validDNSSubdomain(spec.Service) || len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return errors.New("Mirror Service and ports are invalid")
	}
	seen := make(map[string]struct{}, len(spec.Ports))
	for index := range spec.Ports {
		port := &spec.Ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 || (port.Protocol != "tcp" && port.Protocol != "udp") {
			return errors.New("Mirror Service port is invalid")
		}
		key := strconv.Itoa(int(port.ServicePort)) + "/" + port.Protocol
		if _, exists := seen[key]; exists {
			return errors.New("Mirror Service ports must be unique")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateMirrorTask(task MirrorTask, session Session) (MirrorTask, error) {
	if _, err := uuid.Parse(task.ID); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() || net.ParseIP(task.ClusterIP) == nil || task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero() {
		return MirrorTask{}, errors.New("Gateway returned an incomplete Mirror Task")
	}
	spec := MirrorSpec{Service: task.Service, Ports: append([]MirrorPort(nil), task.Ports...)}
	if err := validateMirrorSpec(&spec); err != nil {
		return MirrorTask{}, errors.New("Gateway returned an invalid Mirror Task")
	}
	task.Service, task.Ports = spec.Service, spec.Ports
	return task, nil
}

func validatePreviewSpec(spec *PreviewSpec) error {
	spec.Name = strings.TrimSpace(spec.Name)
	if !validDNSLabel(spec.Name) || len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return errors.New("Preview Service name and ports are invalid")
	}
	seenPorts := make(map[string]struct{}, len(spec.Ports))
	seenNames := make(map[string]struct{}, len(spec.Ports))
	for index := range spec.Ports {
		port := &spec.Ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 || (port.Protocol != "tcp" && port.Protocol != "udp") ||
			(port.Name != "" && !validDNSLabel(port.Name)) {
			return errors.New("Preview Service port is invalid")
		}
		key := strconv.Itoa(int(port.ServicePort)) + "/" + port.Protocol
		if _, exists := seenPorts[key]; exists {
			return errors.New("Preview Service ports must be unique")
		}
		if port.Name != "" {
			if _, exists := seenNames[port.Name]; exists {
				return errors.New("Preview Service port names must be unique")
			}
			seenNames[port.Name] = struct{}{}
		}
		seenPorts[key] = struct{}{}
	}
	return nil
}

func validatePreviewTask(task PreviewTask, session Session) (PreviewTask, error) {
	clusterIP := net.ParseIP(task.ClusterIP)
	if _, err := uuid.Parse(task.ID); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() || (task.ClusterIP != "" && clusterIP == nil) || (task.State == remotetask.Running && clusterIP == nil) ||
		task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() {
		return PreviewTask{}, errors.New("Gateway returned an incomplete Preview Task")
	}
	spec := PreviewSpec{Name: task.Name, Ports: append([]PreviewPort(nil), task.Ports...)}
	if err := validatePreviewSpec(&spec); err != nil {
		return PreviewTask{}, errors.New("Gateway returned an invalid Preview Task")
	}
	task.Name, task.Ports = spec.Name, spec.Ports
	return task, nil
}

func validateExecSpec(spec ExecSpec) error {
	if !validDNSSubdomain(strings.TrimSpace(spec.Pod)) {
		return errors.New("Pod exec target name is invalid")
	}
	if spec.Container != "" && !validDNSLabel(strings.TrimSpace(spec.Container)) {
		return errors.New("Pod exec container name is invalid")
	}
	if len(spec.Command) == 0 || len(spec.Command) > 64 {
		return errors.New("Pod exec command must contain 1 to 64 arguments")
	}
	total := 0
	for _, argument := range spec.Command {
		if argument == "" || len(argument) > 4096 || strings.IndexByte(argument, 0) >= 0 {
			return errors.New("Pod exec command contains an invalid argument")
		}
		total += len(argument)
	}
	if total > 16<<10 {
		return errors.New("Pod exec command exceeds 16 KiB")
	}
	return nil
}

func validateExecTask(task ExecTask, session Session) (ExecTask, error) {
	if _, err := uuid.Parse(task.ID); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() ||
		!validDNSSubdomain(task.Pod) || (task.Container != "" && !validDNSLabel(task.Container)) ||
		task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero() {
		return ExecTask{}, errors.New("Gateway returned an incomplete Pod exec Task")
	}
	return task, nil
}

func validateFileTransferSpec(spec *FileTransferSpec) error {
	spec.Direction = strings.ToLower(strings.TrimSpace(spec.Direction))
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Pod = strings.TrimSpace(spec.Pod)
	spec.Container = strings.TrimSpace(spec.Container)
	spec.Checksum = strings.ToLower(strings.TrimSpace(spec.Checksum))
	spec.ResumeID = strings.ToLower(strings.TrimSpace(spec.ResumeID))
	if spec.Direction != "upload" && spec.Direction != "download" {
		return errors.New("file transfer direction must be upload or download")
	}
	if spec.Kind != "file" && spec.Kind != "directory" {
		return errors.New("file transfer kind must be file or directory")
	}
	if !validDNSSubdomain(spec.Pod) || (spec.Container != "" && !validDNSLabel(spec.Container)) {
		return errors.New("file transfer Pod or container is invalid")
	}
	if err := validateRemotePath(spec.RemotePath); err != nil {
		return err
	}
	if spec.Direction == "upload" {
		if spec.Size == 0 || spec.Offset > spec.Size || !validDigest(spec.Checksum) {
			return errors.New("file upload size, offset or checksum is invalid")
		}
		if spec.Kind == "directory" && spec.Offset != 0 {
			return errors.New("directory upload cannot resume from a byte offset")
		}
		if spec.ResumeID != "" {
			if spec.Kind != "file" {
				return errors.New("only file uploads support a Resume ID")
			}
			if _, err := uuid.Parse(spec.ResumeID); err != nil {
				return errors.New("file upload Resume ID is invalid")
			}
		}
	} else if spec.Size != 0 || spec.Checksum != "" || spec.Overwrite {
		return errors.New("file download metadata must be determined by the Gateway")
	} else if spec.Kind == "directory" && spec.Offset != 0 {
		return errors.New("directory download cannot resume from a byte offset")
	} else if spec.ResumeID != "" {
		return errors.New("file downloads do not accept a Resume ID")
	}
	return nil
}

func validateRemotePath(value string) error {
	if value == "" || len(value) > 4096 || value[0] != '/' || value == "/" || strings.Contains(value, "\\") ||
		path.Clean(value) != value {
		return errors.New("file transfer remote path is invalid")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("file transfer remote path is invalid")
		}
	}
	return nil
}

func validateFileTransferTask(task FileTransferTask, session Session) (FileTransferTask, error) {
	if _, err := uuid.Parse(task.ID); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() ||
		task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero() {
		return FileTransferTask{}, errors.New("Gateway returned an incomplete file transfer Task")
	}
	spec := FileTransferSpec{
		Direction: task.Direction, Kind: task.Kind, Pod: task.Pod, Container: task.Container,
		RemotePath: task.RemotePath, Size: task.Size, Offset: task.Offset, Checksum: task.Checksum, Overwrite: task.Overwrite,
		ResumeID: task.ResumeID,
	}
	if err := validateFileTransferSpec(&spec); err != nil {
		return FileTransferTask{}, errors.New("Gateway returned an invalid file transfer Task")
	}
	task.Direction, task.Kind, task.Pod, task.Container = spec.Direction, spec.Kind, spec.Pod, spec.Container
	task.RemotePath, task.Checksum = spec.RemotePath, spec.Checksum
	task.ResumeID = spec.ResumeID
	return task, nil
}

func validatePodFileSpec(action string, spec *PodFileSpec) error {
	spec.Pod, spec.Container = strings.TrimSpace(spec.Pod), strings.TrimSpace(spec.Container)
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	if !validDNSSubdomain(spec.Pod) || (spec.Container != "" && !validDNSLabel(spec.Container)) {
		return errors.New("remote file Pod or container is invalid")
	}
	if err := validatePodFilePath(spec.Path, action == "list"); err != nil {
		return err
	}
	switch action {
	case "list":
		if spec.Destination != "" || spec.Kind != "" || spec.Recursive {
			return errors.New("remote directory list contains unsupported fields")
		}
	case "create":
		if spec.Kind != "file" && spec.Kind != "directory" {
			return errors.New("remote file create kind must be file or directory")
		}
		if spec.Destination != "" || spec.Recursive {
			return errors.New("remote file create contains unsupported fields")
		}
	case "rename":
		if err := validatePodFilePath(spec.Destination, false); err != nil {
			return errors.New("remote file destination is invalid")
		}
		if spec.Destination == spec.Path || spec.Kind != "" || spec.Recursive {
			return errors.New("remote file rename contains unsupported fields")
		}
	case "delete":
		if spec.Destination != "" || spec.Kind != "" {
			return errors.New("remote file delete contains unsupported fields")
		}
	default:
		return errors.New("remote file action is invalid")
	}
	return nil
}

func validatePodFilePath(value string, allowRoot bool) error {
	if value == "" || len(value) > 4096 || value[0] != '/' || strings.Contains(value, "\\") || path.Clean(value) != value || (!allowRoot && value == "/") {
		return errors.New("remote file path is invalid")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("remote file path is invalid")
		}
	}
	return nil
}

func validatePodFileList(list PodFileList, session Session, requested PodFileSpec) (PodFileList, error) {
	if list.SessionID != session.ID || list.Namespace != session.Namespace || list.Pod != requested.Pod ||
		list.Path != requested.Path || !validDNSLabel(list.Container) || list.Items == nil {
		return PodFileList{}, errors.New("Gateway returned an invalid remote directory listing")
	}
	for _, entry := range list.Items {
		if entry.Name == "" || entry.Name == "." || entry.Name == ".." || path.Base(entry.Path) != entry.Name ||
			path.Dir(entry.Path) != list.Path || (entry.Kind != "file" && entry.Kind != "directory" && entry.Kind != "symlink" && entry.Kind != "other") ||
			entry.Size < 0 || len(entry.Mode) != 4 || entry.ModifiedAt.IsZero() {
			return PodFileList{}, errors.New("Gateway returned an invalid remote directory entry")
		}
		for _, character := range entry.Mode {
			if character < '0' || character > '7' {
				return PodFileList{}, errors.New("Gateway returned an invalid remote directory entry")
			}
		}
	}
	return list, nil
}

func validatePodFileTask(task PodFileTask, session Session) (PodFileTask, error) {
	if _, err := uuid.Parse(task.ID); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() ||
		task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero() {
		return PodFileTask{}, errors.New("Gateway returned an incomplete remote file operation Task")
	}
	spec := PodFileSpec{
		Pod: task.Pod, Container: task.Container, Path: task.Path, Destination: task.Destination,
		Kind: task.Kind, Recursive: task.Recursive,
	}
	if err := validatePodFileSpec(task.Action, &spec); err != nil {
		return PodFileTask{}, errors.New("Gateway returned an invalid remote file operation Task")
	}
	if task.State == remotetask.Stopped && (!task.Result.Completed || task.Result.Error != "") {
		return PodFileTask{}, errors.New("Gateway returned an invalid remote file operation result")
	}
	if task.State == "failed" && (task.Result.Completed || strings.TrimSpace(task.Result.Error) == "") {
		return PodFileTask{}, errors.New("Gateway returned an invalid remote file operation result")
	}
	return task, nil
}

func validDNSLabel(value string) bool {
	return !strings.Contains(value, ".") && validDNSSubdomain(value)
}

func generationHeader(generation uint64) http.Header {
	return http.Header{"If-Match": {fmt.Sprintf("\"%d\"", generation)}}
}
