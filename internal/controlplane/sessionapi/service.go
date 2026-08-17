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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionregistry"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
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
	Discover(context.Context, controlplaneapi.Identity, string) (networkspec.Spec, error)
}

type CapabilityDiscoverer interface {
	DiscoverCapabilities(context.Context, controlplaneapi.Identity, string) (capability.Snapshot, *controlplaneapi.Error)
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

type Service struct {
	storage      Storage
	clusterID    string
	sessionTTL   time.Duration
	maxLifetime  time.Duration
	now          func() time.Time
	networks     NetworkDiscoverer
	capabilities CapabilityDiscoverer
	registry     *sessionregistry.Registry
}

func New(storageBackend Storage, config Config) (*Service, error) {
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
	return &Service{
		storage: storageBackend, clusterID: config.ClusterID, sessionTTL: config.SessionTTL,
		maxLifetime: config.MaxLifetime, now: config.Now, networks: config.Networks,
		capabilities: config.Capabilities, registry: config.Registry,
	}, nil
}

func (handler *Service) RequireActive(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) (ActiveSession, *controlplaneapi.Error) {
	session, apiError := handler.loadOwned(ctx, identity, namespace, id)
	if apiError != nil {
		return ActiveSession{}, apiError
	}
	if session.State != "active" || !session.ExpiresAt.After(handler.now().UTC()) {
		return ActiveSession{}, &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Session is not active"}
	}
	if session.NetworkSpecHash == "" {
		return ActiveSession{}, &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Session has no NetworkSpec"}
	}
	if err := handler.registry.Ensure(session.ID); err != nil {
		return ActiveSession{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Session runtime is unavailable", Cause: err}
	}
	return ActiveSession{
		ID: session.ID, Namespace: session.Namespace, Generation: session.Generation, ExpiresAt: session.ExpiresAt,
		NetworkSpecHash: session.NetworkSpecHash,
	}, nil
}

func (handler *Service) create(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	namespace string,
) *controlplaneapi.Error {
	request := ctx.Request()
	if strings.TrimSpace(identity.Subject) == "" || strings.TrimSpace(identity.DeviceID) == "" {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "authenticated device identity is required"}
	}
	idempotencyKey, apiError := idempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	now := handler.now().UTC()
	capabilitySnapshot, apiError := handler.capabilities.DiscoverCapabilities(request.Context(), identity, namespace)
	if apiError != nil {
		return apiError
	}
	spec, err := handler.networks.Discover(request.Context(), identity, namespace)
	if err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Kubernetes NetworkSpec discovery failed", Cause: err}
	}
	specJSON, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "NetworkSpec validation failed", Cause: err}
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "NetworkSpec validation failed", Cause: err}
	}
	session := storage.Session{
		ID: uuid.NewString(), IdentityID: identity.Subject, DeviceID: identity.DeviceID,
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
			Scope: "session:create:" + identity.Subject, Key: idempotencyKey, RequestHash: requestHash,
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
			if !ownedBy(existing, identity, namespace) {
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
		return &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Session runtime is unavailable", Cause: err}
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	ctx.Response().Header().Set("Location", controlplane.SessionAPIPathPrefix+"/sessions/"+session.ID+"?namespace="+namespace)
	if !created {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
	}
	writeDocument(ctx, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], documentWithCapabilities(session, capabilitySnapshot))
	return nil
}

func (handler *Service) get(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) *controlplaneapi.Error {
	request := ctx.Request()
	session, apiError := handler.loadOwned(request.Context(), identity, namespace, id)
	if apiError != nil {
		return apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	writeDocument(ctx, http.StatusOK, documentFromSession(session))
	return nil
}

func (handler *Service) heartbeat(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) *controlplaneapi.Error {
	request := ctx.Request()
	generation, apiError := expectedGeneration(request)
	if apiError != nil {
		return apiError
	}
	session, apiError := handler.loadOwned(request.Context(), identity, namespace, id)
	if apiError != nil {
		return apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	now := handler.now().UTC()
	if session.State != "active" || !session.ExpiresAt.After(now) {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Session is not active"}
	}
	currentCapabilities, capabilityError := handler.capabilities.DiscoverCapabilities(request.Context(), identity, namespace)
	if capabilityError != nil {
		return capabilityError
	}
	if !slices.Contains(currentCapabilities.Capabilities, "cluster.tunnel") {
		if err := handler.storage.Sessions().UpdateState(request.Context(), session.ID, generation, "disconnected", now); err != nil {
			return mapStorageError(err)
		}
		if apiError := handler.disconnectRuntime(request.Context(), session.ID); apiError != nil {
			return apiError
		}
		return &controlplaneapi.Error{Code: controlplaneapi.CodeForbidden, Message: "Session access was revoked"}
	}
	maximumExpiry := session.CreatedAt.Add(handler.maxLifetime)
	nextExpiry := now.Add(handler.sessionTTL)
	if maximumExpiry.Before(nextExpiry) {
		nextExpiry = maximumExpiry
	}
	if !nextExpiry.After(now) {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Session maximum lifetime has elapsed"}
	}
	spec, err := handler.networks.Discover(request.Context(), identity, namespace)
	if err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Kubernetes NetworkSpec refresh failed", Cause: err}
	}
	specJSON, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "NetworkSpec validation failed", Cause: err}
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "NetworkSpec validation failed", Cause: err}
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
	writeDocument(ctx, http.StatusOK, documentFromSession(session))
	return nil
}

func (handler *Service) disconnect(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) *controlplaneapi.Error {
	request := ctx.Request()
	generation, apiError := expectedGeneration(request)
	if apiError != nil {
		return apiError
	}
	session, apiError := handler.loadOwned(request.Context(), identity, namespace, id)
	if apiError != nil {
		return apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	if session.State == "disconnected" || session.State == "expired" {
		if apiError := handler.disconnectRuntime(request.Context(), session.ID); apiError != nil {
			return apiError
		}
		writeDocument(ctx, http.StatusOK, documentFromSession(session))
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
	writeDocument(ctx, http.StatusOK, documentFromSession(session))
	return nil
}

// AttachRuntime is consumed by streamlease without coupling feature handlers
// to the concrete Registry implementation.
func (handler *Service) AttachRuntime(
	parent context.Context,
	sessionID, taskID string,
) (context.Context, func(), error) {
	return handler.registry.Attach(parent, sessionID, taskID)
}

func (handler *Service) disconnectRuntime(parent context.Context, sessionID string) *controlplaneapi.Error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := handler.registry.Disconnect(ctx, sessionID); err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Session runtime cleanup is pending", Cause: err}
	}
	if err := handler.settleOwnedTasks(ctx, sessionID); err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Session Task cleanup is pending", Cause: err}
	}
	return nil
}

func (handler *Service) settleOwnedTasks(ctx context.Context, sessionID string) error {
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

func (handler *Service) loadOwned(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) (storage.Session, *controlplaneapi.Error) {
	if _, err := uuid.Parse(id); err != nil {
		return storage.Session{}, notFound()
	}
	session, err := handler.storage.Sessions().GetByID(ctx, id)
	if err != nil || !ownedBy(session, identity, namespace) {
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

func ownedBy(session storage.Session, identity controlplaneapi.Identity, namespace string) bool {
	return session.IdentityID == identity.Subject && session.DeviceID == identity.DeviceID && session.Namespace == namespace
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

func writeDocument(ctx *echo.Context, status int, document Document) {
	ctx.Response().Header().Set("ETag", fmt.Sprintf("\"%d\"", document.Generation))
	_ = ctx.JSON(status, document)
}

func namespaceFromQuery(request *http.Request) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "namespace" {
			return "", &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: key, Message: "query parameter is not supported"}
		}
		if len(values) != 1 {
			return "", &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: key, Message: "query parameter must be provided once"}
		}
	}
	namespace := query.Get("namespace")
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return "", &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "namespace", Message: "namespace is invalid"}
	}
	return namespace, nil
}

func idempotencyKey(request *http.Request) (string, *controlplaneapi.Error) {
	values := request.Header.Values(IdempotencyHeader)
	if len(values) != 1 {
		return "", &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: IdempotencyHeader, Message: "Idempotency-Key must be provided once"}
	}
	key := strings.TrimSpace(values[0])
	if key == "" || len(key) > 128 {
		return "", &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: IdempotencyHeader, Message: "Idempotency-Key is invalid"}
	}
	for _, character := range key {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._:", character) {
			continue
		}
		return "", &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: IdempotencyHeader, Message: "Idempotency-Key is invalid"}
	}
	return key, nil
}

func expectedGeneration(request *http.Request) (uint64, *controlplaneapi.Error) {
	values := request.Header.Values("If-Match")
	if len(values) != 1 {
		return 0, &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "If-Match", Message: "If-Match generation is required"}
	}
	raw := strings.TrimSpace(values[0])
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return 0, &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "If-Match", Message: "If-Match generation is invalid"}
	}
	generation, err := strconv.ParseUint(raw[1:len(raw)-1], 10, 64)
	if err != nil || generation == 0 {
		return 0, &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "If-Match", Message: "If-Match generation is invalid"}
	}
	return generation, nil
}

func requireEmptyBody(request *http.Request) *controlplaneapi.Error {
	contents, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Message: "request body is invalid"}
	}
	if len(contents) != 0 {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Message: "request body must be empty"}
	}
	return nil
}

func mapStorageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Session state changed; reload and retry", Cause: err}
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Idempotency-Key was already used for a different request", Cause: err}
	default:
		return internalError(err)
	}
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "Session operation failed", Cause: err}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
}
