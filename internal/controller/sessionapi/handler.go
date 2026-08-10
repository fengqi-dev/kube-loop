package sessionapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionregistry"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	DefaultSessionTTL  = 2 * time.Minute
	DefaultMaxLifetime = 8 * time.Hour
	IdempotencyHeader  = "Idempotency-Key"
)

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type NetworkDiscoverer interface {
	Discover(context.Context, controller.Principal, string) (networkspec.Spec, error)
}

type CapabilityDiscoverer interface {
	DiscoverCapabilities(context.Context, controller.Principal, string) (capability.Snapshot, *controller.APIError)
}

type Config struct {
	ClusterID    string
	SessionTTL   time.Duration
	MaxLifetime  time.Duration
	Now          func() time.Time
	Networks     NetworkDiscoverer
	Capabilities CapabilityDiscoverer
	Registry     *sessionregistry.Registry
}

type Handler struct {
	storage      Storage
	clusterID    string
	sessionTTL   time.Duration
	maxLifetime  time.Duration
	now          func() time.Time
	networks     NetworkDiscoverer
	capabilities CapabilityDiscoverer
	registry     *sessionregistry.Registry
}

type Document struct {
	ID              string               `json:"id"`
	Namespace       string               `json:"namespace"`
	State           string               `json:"state"`
	Generation      uint64               `json:"generation"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt       time.Time            `json:"updatedAt"`
	LastHeartbeatAt time.Time            `json:"lastHeartbeatAt"`
	ExpiresAt       time.Time            `json:"expiresAt"`
	NetworkSpec     networkspec.Spec     `json:"networkSpec"`
	NetworkSpecHash string               `json:"networkSpecHash"`
	Capabilities    *capability.Snapshot `json:"capabilities,omitempty"`
}

type ActiveSession struct {
	ID              string
	Namespace       string
	Generation      uint64
	ExpiresAt       time.Time
	NetworkSpecHash string
}

func New(storageBackend Storage, config Config) (*Handler, error) {
	if storageBackend == nil || config.Networks == nil || config.Capabilities == nil {
		return nil, errors.New("Session storage, NetworkSpec and capability discoverers are required")
	}
	config.ClusterID = strings.TrimSpace(config.ClusterID)
	if config.ClusterID == "" || len(config.ClusterID) > 256 {
		return nil, errors.New("Session cluster ID is required")
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = DefaultSessionTTL
	}
	if config.SessionTTL < 30*time.Second || config.SessionTTL > 30*time.Minute {
		return nil, errors.New("Session TTL must be between 30 seconds and 30 minutes")
	}
	if config.MaxLifetime <= 0 {
		config.MaxLifetime = DefaultMaxLifetime
	}
	if config.MaxLifetime < config.SessionTTL || config.MaxLifetime > 24*time.Hour {
		return nil, errors.New("Session maximum lifetime must be between the TTL and 24 hours")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Registry == nil {
		config.Registry = sessionregistry.New(context.Background())
	}
	return &Handler{
		storage: storageBackend, clusterID: config.ClusterID, sessionTTL: config.SessionTTL,
		maxLifetime: config.MaxLifetime, now: config.Now, networks: config.Networks,
		capabilities: config.Capabilities, registry: config.Registry,
	}, nil
}

func (handler *Handler) ServeAPI(writer http.ResponseWriter, request *http.Request, principal controller.Principal) *controller.APIError {
	parts, valid := routeParts(request.URL.Path)
	if !valid {
		return notFound()
	}
	namespace, apiError := namespaceFromQuery(request)
	if apiError != nil {
		return apiError
	}
	if apiError := requireEmptyBody(request); apiError != nil {
		return apiError
	}
	switch {
	case len(parts) == 1 && request.Method == http.MethodPost:
		return handler.create(writer, request, principal, namespace)
	case len(parts) == 2 && request.Method == http.MethodGet:
		return handler.get(writer, request, principal, namespace, parts[1])
	case len(parts) == 3 && parts[2] == "heartbeat" && request.Method == http.MethodPost:
		return handler.heartbeat(writer, request, principal, namespace, parts[1])
	case len(parts) == 2 && request.Method == http.MethodDelete:
		return handler.disconnect(writer, request, principal, namespace, parts[1])
	default:
		return notFound()
	}
}

func (handler *Handler) RequireActive(
	ctx context.Context,
	principal controller.Principal,
	namespace, id string,
) (ActiveSession, *controller.APIError) {
	session, apiError := handler.loadOwned(ctx, principal, namespace, id)
	if apiError != nil {
		return ActiveSession{}, apiError
	}
	if session.State != "active" || !session.ExpiresAt.After(handler.now().UTC()) {
		return ActiveSession{}, &controller.APIError{Code: controller.CodeConflict, Message: "Session is not active"}
	}
	if session.NetworkSpecHash == "" {
		return ActiveSession{}, &controller.APIError{Code: controller.CodeConflict, Message: "Session has no NetworkSpec"}
	}
	if err := handler.registry.Ensure(session.ID); err != nil {
		return ActiveSession{}, &controller.APIError{Code: controller.CodeUnavailable, Message: "Session runtime is unavailable", Cause: err}
	}
	return ActiveSession{
		ID: session.ID, Namespace: session.Namespace, Generation: session.Generation, ExpiresAt: session.ExpiresAt,
		NetworkSpecHash: session.NetworkSpecHash,
	}, nil
}

func (handler *Handler) create(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	namespace string,
) *controller.APIError {
	if strings.TrimSpace(principal.Subject) == "" || strings.TrimSpace(principal.DeviceID) == "" {
		return &controller.APIError{Code: controller.CodeUnauthenticated, Message: "authenticated device identity is required"}
	}
	idempotencyKey, apiError := idempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	now := handler.now().UTC()
	capabilitySnapshot, apiError := handler.capabilities.DiscoverCapabilities(request.Context(), principal, namespace)
	if apiError != nil {
		return apiError
	}
	spec, err := handler.networks.Discover(request.Context(), principal, namespace)
	if err != nil {
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Kubernetes NetworkSpec discovery failed", Cause: err}
	}
	specJSON, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		return &controller.APIError{Code: controller.CodeInternal, Message: "NetworkSpec validation failed", Cause: err}
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		return &controller.APIError{Code: controller.CodeInternal, Message: "NetworkSpec validation failed", Cause: err}
	}
	session := storage.Session{
		ID: uuid.NewString(), PrincipalID: principal.Subject, DeviceID: principal.DeviceID,
		ClusterID: handler.clusterID, Namespace: namespace, State: "active",
		Generation:  1,
		NetworkSpec: specJSON, NetworkSpecHash: specHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(handler.sessionTTL),
	}
	document := documentWithCapabilities(session, capabilitySnapshot)
	responseJSON, err := json.Marshal(document)
	if err != nil {
		return internalError(err)
	}
	requestHashRaw := sha256.Sum256([]byte("session-create-v1\n" + namespace))
	requestHash := hex.EncodeToString(requestHashRaw[:])
	created := false
	err = handler.storage.WithinTransaction(request.Context(), func(repositories storage.Repositories) error {
		record, reserved, reserveErr := repositories.Idempotency().Reserve(request.Context(), storage.IdempotencyRecord{
			Scope: "session:create:" + principal.Subject, Key: idempotencyKey, RequestHash: requestHash,
			ResourceType: "session", ResourceID: session.ID, Response: responseJSON,
			CreatedAt: now, ExpiresAt: now.Add(handler.maxLifetime),
		})
		if reserveErr != nil {
			return reserveErr
		}
		if !reserved {
			if record.ResourceType != "session" {
				return storage.ErrConflict
			}
			existing, getErr := repositories.Sessions().GetByID(request.Context(), record.ResourceID)
			if getErr != nil {
				return getErr
			}
			if !ownedBy(existing, principal, namespace) {
				return storage.ErrNotFound
			}
			session = existing
			return nil
		}
		if createErr := repositories.Sessions().Create(request.Context(), session); createErr != nil {
			return createErr
		}
		created = true
		return nil
	})
	if err != nil {
		return mapStorageError(err)
	}
	if err := handler.registry.Ensure(session.ID); err != nil {
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Session runtime is unavailable", Cause: err}
	}
	controller.SetAuditSessionID(request.Context(), session.ID)
	writer.Header().Set("Location", controller.APIPathPrefix+"/sessions/"+session.ID+"?namespace="+namespace)
	if !created {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeDocument(writer, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], documentWithCapabilities(session, capabilitySnapshot))
	return nil
}

func (handler *Handler) get(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	namespace, id string,
) *controller.APIError {
	session, apiError := handler.loadOwned(request.Context(), principal, namespace, id)
	if apiError != nil {
		return apiError
	}
	controller.SetAuditSessionID(request.Context(), session.ID)
	writeDocument(writer, http.StatusOK, documentFromSession(session))
	return nil
}

func (handler *Handler) heartbeat(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	namespace, id string,
) *controller.APIError {
	generation, apiError := expectedGeneration(request)
	if apiError != nil {
		return apiError
	}
	session, apiError := handler.loadOwned(request.Context(), principal, namespace, id)
	if apiError != nil {
		return apiError
	}
	controller.SetAuditSessionID(request.Context(), session.ID)
	now := handler.now().UTC()
	if session.State != "active" || !session.ExpiresAt.After(now) {
		return &controller.APIError{Code: controller.CodeConflict, Message: "Session is not active"}
	}
	maximumExpiry := session.CreatedAt.Add(handler.maxLifetime)
	nextExpiry := now.Add(handler.sessionTTL)
	if maximumExpiry.Before(nextExpiry) {
		nextExpiry = maximumExpiry
	}
	if !nextExpiry.After(now) {
		return &controller.APIError{Code: controller.CodeConflict, Message: "Session maximum lifetime has elapsed"}
	}
	spec, err := handler.networks.Discover(request.Context(), principal, namespace)
	if err != nil {
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Kubernetes NetworkSpec refresh failed", Cause: err}
	}
	specJSON, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		return &controller.APIError{Code: controller.CodeInternal, Message: "NetworkSpec validation failed", Cause: err}
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		return &controller.APIError{Code: controller.CodeInternal, Message: "NetworkSpec validation failed", Cause: err}
	}
	if err := handler.storage.Sessions().Heartbeat(
		request.Context(), session.ID, generation, specJSON, specHash, now, nextExpiry,
	); err != nil {
		return mapStorageError(err)
	}
	session, err = handler.storage.Sessions().GetByID(request.Context(), session.ID)
	if err != nil {
		return mapStorageError(err)
	}
	writeDocument(writer, http.StatusOK, documentFromSession(session))
	return nil
}

func (handler *Handler) disconnect(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	namespace, id string,
) *controller.APIError {
	generation, apiError := expectedGeneration(request)
	if apiError != nil {
		return apiError
	}
	session, apiError := handler.loadOwned(request.Context(), principal, namespace, id)
	if apiError != nil {
		return apiError
	}
	controller.SetAuditSessionID(request.Context(), session.ID)
	if session.State == "disconnected" || session.State == "expired" {
		if apiError := handler.disconnectRuntime(request.Context(), session.ID); apiError != nil {
			return apiError
		}
		writeDocument(writer, http.StatusOK, documentFromSession(session))
		return nil
	}
	if err := handler.storage.Sessions().UpdateState(request.Context(), session.ID, generation, "disconnected", handler.now().UTC()); err != nil {
		return mapStorageError(err)
	}
	if apiError := handler.disconnectRuntime(request.Context(), session.ID); apiError != nil {
		return apiError
	}
	session, err := handler.storage.Sessions().GetByID(request.Context(), session.ID)
	if err != nil {
		return mapStorageError(err)
	}
	writeDocument(writer, http.StatusOK, documentFromSession(session))
	return nil
}

// AttachRuntime is consumed by streamlease without coupling feature handlers
// to the concrete Registry implementation.
func (handler *Handler) AttachRuntime(
	parent context.Context,
	sessionID, taskID string,
) (context.Context, func(), error) {
	return handler.registry.Attach(parent, sessionID, taskID)
}

func (handler *Handler) disconnectRuntime(parent context.Context, sessionID string) *controller.APIError {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := handler.registry.Disconnect(ctx, sessionID); err != nil {
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Session runtime cleanup is pending", Cause: err}
	}
	if err := handler.settleOwnedTasks(ctx, sessionID); err != nil {
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Session Task cleanup is pending", Cause: err}
	}
	return nil
}

func (handler *Handler) settleOwnedTasks(ctx context.Context, sessionID string) error {
	tasks, err := handler.storage.Tasks().ListBySession(ctx, sessionID, 1000)
	if err != nil {
		return err
	}
	var result error
	for _, task := range tasks {
		if task.State.Terminal() {
			continue
		}
		resourceBacked := task.Type == "exchange" || task.Type == "mirror" || task.Type == "preview"
		if resourceBacked && task.State == remotetask.Recovering {
			// The feature-specific recovery worker owns this state and its
			// heartbeat; do not postpone its stale-owner boundary on repeated GET.
			continue
		}
		next := remotetask.Failed
		switch {
		case task.State == remotetask.Pending || task.Type == "port-forward":
			next = remotetask.Stopped
		case resourceBacked:
			next = remotetask.Recovering
		case task.State == remotetask.Stopping:
			next = remotetask.Stopped
		}
		if updateErr := handler.storage.Tasks().UpdateState(
			ctx, task.ID, task.State, next, task.Result, handler.now().UTC(),
		); updateErr != nil && !errors.Is(updateErr, storage.ErrConflict) && !errors.Is(updateErr, storage.ErrNotFound) {
			result = errors.Join(result, updateErr)
		}
	}
	return result
}

func (handler *Handler) loadOwned(
	ctx context.Context,
	principal controller.Principal,
	namespace, id string,
) (storage.Session, *controller.APIError) {
	if _, err := uuid.Parse(id); err != nil {
		return storage.Session{}, notFound()
	}
	session, err := handler.storage.Sessions().GetByID(ctx, id)
	if err != nil || !ownedBy(session, principal, namespace) {
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return storage.Session{}, mapStorageError(err)
		}
		return storage.Session{}, notFound()
	}
	now := handler.now().UTC()
	if session.State == "active" && !session.ExpiresAt.After(now) {
		if updateErr := handler.storage.Sessions().UpdateState(ctx, session.ID, session.Generation, "expired", now); updateErr == nil {
			session, err = handler.storage.Sessions().GetByID(ctx, session.ID)
			if err != nil {
				return storage.Session{}, mapStorageError(err)
			}
		} else if errors.Is(updateErr, storage.ErrConflict) {
			session, err = handler.storage.Sessions().GetByID(ctx, session.ID)
			if err != nil {
				return storage.Session{}, mapStorageError(err)
			}
		} else {
			return storage.Session{}, mapStorageError(updateErr)
		}
	}
	if session.State != "active" {
		if apiError := handler.disconnectRuntime(ctx, session.ID); apiError != nil {
			return storage.Session{}, apiError
		}
	}
	return session, nil
}

func ownedBy(session storage.Session, principal controller.Principal, namespace string) bool {
	return session.PrincipalID == principal.Subject && session.DeviceID == principal.DeviceID && session.Namespace == namespace
}

func documentFromSession(session storage.Session) Document {
	spec, _ := networkspec.Decode(session.NetworkSpec)
	return Document{
		ID: session.ID, Namespace: session.Namespace, State: session.State, Generation: session.Generation,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
		LastHeartbeatAt: session.LastHeartbeatAt, ExpiresAt: session.ExpiresAt,
		NetworkSpec: spec, NetworkSpecHash: session.NetworkSpecHash,
	}
}

func documentWithCapabilities(session storage.Session, snapshot capability.Snapshot) Document {
	document := documentFromSession(session)
	document.Capabilities = &snapshot
	return document
}

func writeDocument(writer http.ResponseWriter, status int, document Document) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("ETag", fmt.Sprintf("\"%d\"", document.Generation))
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(document)
}

func routeParts(path string) ([]string, bool) {
	suffix, ok := strings.CutPrefix(path, controller.APIPathPrefix+"/sessions")
	if !ok || strings.HasSuffix(suffix, "/") || strings.Contains(suffix, "//") {
		return nil, false
	}
	if suffix == "" {
		return []string{"sessions"}, true
	}
	if !strings.HasPrefix(suffix, "/") {
		return nil, false
	}
	return append([]string{"sessions"}, strings.Split(suffix[1:], "/")...), true
}

func namespaceFromQuery(request *http.Request) (string, *controller.APIError) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "namespace" {
			return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: key, Message: "query parameter is not supported"}
		}
		if len(values) != 1 {
			return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: key, Message: "query parameter must be provided once"}
		}
	}
	namespace := query.Get("namespace")
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: "namespace", Message: "namespace is invalid"}
	}
	return namespace, nil
}

func idempotencyKey(request *http.Request) (string, *controller.APIError) {
	values := request.Header.Values(IdempotencyHeader)
	if len(values) != 1 {
		return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: IdempotencyHeader, Message: "Idempotency-Key must be provided once"}
	}
	key := strings.TrimSpace(values[0])
	if key == "" || len(key) > 128 {
		return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: IdempotencyHeader, Message: "Idempotency-Key is invalid"}
	}
	for _, character := range key {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._:", character) {
			continue
		}
		return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: IdempotencyHeader, Message: "Idempotency-Key is invalid"}
	}
	return key, nil
}

func expectedGeneration(request *http.Request) (uint64, *controller.APIError) {
	values := request.Header.Values("If-Match")
	if len(values) != 1 {
		return 0, &controller.APIError{Code: controller.CodeInvalidArgument, Field: "If-Match", Message: "If-Match generation is required"}
	}
	raw := strings.TrimSpace(values[0])
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return 0, &controller.APIError{Code: controller.CodeInvalidArgument, Field: "If-Match", Message: "If-Match generation is invalid"}
	}
	generation, err := strconv.ParseUint(raw[1:len(raw)-1], 10, 64)
	if err != nil || generation == 0 {
		return 0, &controller.APIError{Code: controller.CodeInvalidArgument, Field: "If-Match", Message: "If-Match generation is invalid"}
	}
	return generation, nil
}

func requireEmptyBody(request *http.Request) *controller.APIError {
	contents, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Message: "request body is invalid"}
	}
	if len(contents) != 0 {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Message: "request body must be empty"}
	}
	return nil
}

func mapStorageError(err error) *controller.APIError {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict):
		return &controller.APIError{Code: controller.CodeConflict, Message: "Session state changed; reload and retry", Cause: err}
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controller.APIError{Code: controller.CodeConflict, Message: "Idempotency-Key was already used for a different request", Cause: err}
	default:
		return internalError(err)
	}
}

func internalError(err error) *controller.APIError {
	return &controller.APIError{Code: controller.CodeInternal, Message: "Session operation failed", Cause: err}
}

func notFound() *controller.APIError {
	return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
}
