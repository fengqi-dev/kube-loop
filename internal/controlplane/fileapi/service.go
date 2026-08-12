package fileapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	TaskType          = "file-transfer"
	DirectionUpload   = "upload"
	DirectionDownload = "download"
	KindFile          = "file"
	KindDirectory     = "directory"
	defaultMaxBytes   = uint64(1 << 30)
)

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionValidator interface {
	RequireActive(context.Context, controlplaneapi.Principal, string, string) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type TargetResolver interface {
	ResolveContainer(context.Context, controlplaneapi.Principal, string, string, string) (string, error)
}

type Config struct {
	Now                     func() time.Time
	MaximumBytes            uint64
	AllowedPathRoots        []string
	CredentialCheckInterval time.Duration
}

type Service struct {
	storage                 Storage
	sessions                SessionValidator
	targets                 TargetResolver
	executor                TransferExecutor
	now                     func() time.Time
	maximumBytes            uint64
	allowedRoots            []string
	credentialCheckInterval time.Duration
}

func New(
	storageBackend Storage,
	sessions SessionValidator,
	targets TargetResolver,
	executor TransferExecutor,
	config Config,
) (*Service, error) {
	if storageBackend == nil || sessions == nil || targets == nil || executor == nil {
		return nil, errors.New("file transfer storage, Session validator, target resolver and executor are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.MaximumBytes == 0 {
		config.MaximumBytes = defaultMaxBytes
	}
	if config.MaximumBytes < filestream.MaximumData || config.MaximumBytes > 1<<40 {
		return nil, errors.New("file transfer maximum size must be between 256 KiB and 1 TiB")
	}
	if config.CredentialCheckInterval == 0 {
		config.CredentialCheckInterval = 5 * time.Second
	}
	if config.CredentialCheckInterval < 10*time.Millisecond || config.CredentialCheckInterval > 30*time.Second {
		return nil, errors.New("file transfer credential check interval must be between 10ms and 30s")
	}
	roots, err := normalizeRoots(config.AllowedPathRoots)
	if err != nil {
		return nil, err
	}
	return &Service{
		storage: storageBackend, sessions: sessions, targets: targets, executor: executor, now: config.Now,
		maximumBytes: config.MaximumBytes, allowedRoots: roots, credentialCheckInterval: config.CredentialCheckInterval,
	}, nil
}

func (handler *Service) create(
	ctx *echo.Context,
	principal controlplaneapi.Principal,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	request := ctx.Request()
	var spec Spec
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	if apiError := handler.normalizeSpec(&spec); apiError != nil {
		return apiError
	}
	key, apiError := taskapi.IdempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	requestHash, err := taskapi.RequestHash(session.ID, session.Namespace, spec)
	if err != nil {
		return internalError(err)
	}
	scope := taskapi.Scope(TaskType, principal.Subject)
	if record, err := handler.storage.Idempotency().Get(request.Context(), scope, key); err == nil {
		if record.RequestHash != requestHash {
			return storageError(storage.ErrIdempotencyMismatch)
		}
		task, err := handler.storage.Tasks().GetByID(request.Context(), record.ResourceID)
		if err != nil || !owned(task, principal, session) {
			return notFound()
		}
		document, err := handler.decodeTask(task, session.Namespace)
		if err != nil {
			return internalError(err)
		}
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
		writeJSON(ctx, http.StatusOK, document)
		return nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return storageError(err)
	}
	container, err := handler.targets.ResolveContainer(
		request.Context(), principal, session.Namespace, spec.Pod, spec.Container,
	)
	if err != nil {
		return targetError(err)
	}
	spec.Container = container
	if spec.ResumeID != "" {
		spec.Offset, err = handler.executor.UploadOffset(request.Context(), principal, session.Namespace, spec)
		if err != nil {
			return targetError(err)
		}
		if spec.Offset > spec.Size {
			return invalid("resumeId", "remote partial upload exceeds the declared size")
		}
	}
	specJSON, _ := json.Marshal(spec)
	now := handler.now().UTC()
	expiresAt := session.ExpiresAt.UTC()
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: principal.Subject, SessionID: session.ID,
		Type: TaskType, State: remotetask.Pending, Spec: specJSON, IdempotencyKey: key,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}
	document := handler.documentFromTask(task, session.Namespace)
	response, _ := json.Marshal(document)
	created := false
	err = handler.storage.WithinTransaction(request.Context(), func(repositories storage.Repositories) error {
		record, reserved, err := repositories.Idempotency().Reserve(request.Context(), storage.IdempotencyRecord{
			Scope: scope, Key: key, RequestHash: requestHash, ResourceType: TaskType,
			ResourceID: task.ID, Response: response, CreatedAt: now, ExpiresAt: expiresAt,
		})
		if err != nil {
			return err
		}
		if !reserved {
			existing, err := repositories.Tasks().GetByID(request.Context(), record.ResourceID)
			if err != nil || !owned(existing, principal, session) {
				return storage.ErrNotFound
			}
			task = existing
			return nil
		}
		if err := repositories.Tasks().Create(request.Context(), task); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return storageError(err)
	}
	document, err = handler.decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	ctx.Response().Header().Set("Location", fmt.Sprintf("%s/sessions/%s/file-transfers/%s/stream?namespace=%s", controlplane.APIPathPrefix, session.ID, task.ID, session.Namespace))
	if !created {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(ctx, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], document)
	return nil
}

func (handler *Service) get(
	ctx *echo.Context,
	principal controlplaneapi.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	request := ctx.Request()
	if _, err := uuid.Parse(taskID); err != nil {
		return notFound()
	}
	task, err := handler.storage.Tasks().GetByID(request.Context(), taskID)
	if err != nil || !owned(task, principal, session) {
		return notFound()
	}
	document, err := handler.decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(ctx, http.StatusOK, document)
	return nil
}

func (handler *Service) normalizeSpec(spec *Spec) *controlplaneapi.Error {
	spec.Direction = strings.TrimSpace(strings.ToLower(spec.Direction))
	spec.Kind = strings.TrimSpace(strings.ToLower(spec.Kind))
	spec.Pod = strings.TrimSpace(spec.Pod)
	spec.Container = strings.TrimSpace(spec.Container)
	spec.Checksum = strings.TrimSpace(strings.ToLower(spec.Checksum))
	spec.ResumeID = strings.TrimSpace(strings.ToLower(spec.ResumeID))
	if spec.Direction != DirectionUpload && spec.Direction != DirectionDownload {
		return invalid("direction", "direction must be upload or download")
	}
	if spec.Kind != KindFile && spec.Kind != KindDirectory {
		return invalid("kind", "kind must be file or directory")
	}
	if len(validation.IsDNS1123Subdomain(spec.Pod)) != 0 {
		return invalid("pod", "Pod name is invalid")
	}
	if spec.Container != "" && len(validation.IsDNS1123Label(spec.Container)) != 0 {
		return invalid("container", "container name is invalid")
	}
	remotePath, err := normalizeRemotePath(spec.RemotePath, handler.allowedRoots)
	if err != nil {
		return invalid("remotePath", err.Error())
	}
	spec.RemotePath = remotePath
	spec.AllowedRoot = matchingAllowedRoot(remotePath, handler.allowedRoots)
	if spec.AllowedRoot == "" {
		return invalid("remotePath", "container path is outside the configured allowed roots")
	}
	if spec.Offset > handler.maximumBytes {
		return invalid("offset", "offset exceeds the configured transfer limit")
	}
	if spec.Direction == DirectionUpload {
		if spec.Size == 0 || spec.Size > handler.maximumBytes || spec.Offset > spec.Size {
			return invalid("size", "upload size or offset is invalid")
		}
		if _, err := filestream.ParseChecksum(spec.Checksum); err != nil {
			return invalid("checksum", err.Error())
		}
		if spec.Kind == KindDirectory && spec.Offset != 0 {
			return invalid("offset", "directory upload cannot resume from a byte offset")
		}
		if spec.ResumeID != "" {
			if spec.Kind != KindFile {
				return invalid("resumeId", "only file uploads support a Resume ID")
			}
			if _, err := uuid.Parse(spec.ResumeID); err != nil {
				return invalid("resumeId", "upload Resume ID is invalid")
			}
		}
	} else if spec.Size != 0 || spec.Checksum != "" || spec.Overwrite {
		return invalid("direction", "download metadata is determined by the Gateway")
	} else if spec.Kind == KindDirectory && spec.Offset != 0 {
		return invalid("offset", "directory download cannot resume from a byte offset")
	} else if spec.ResumeID != "" {
		return invalid("resumeId", "downloads do not accept a Resume ID")
	}
	return nil
}

func (handler *Service) specFromTask(task storage.Task) (Spec, error) {
	var spec Spec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return Spec{}, errors.New("decode file transfer Task")
	}
	if apiError := handler.normalizeSpec(&spec); apiError != nil || spec.Container == "" {
		return Spec{}, errors.New("stored file transfer Task is invalid")
	}
	return spec, nil
}

func (handler *Service) documentFromTask(task storage.Task, namespace string) Document {
	document, _ := handler.decodeTask(task, namespace)
	return document
}

func (handler *Service) decodeTask(task storage.Task, namespace string) (Document, error) {
	spec, err := handler.specFromTask(task)
	if err != nil {
		return Document{}, err
	}
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	return Document{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State,
		Direction: spec.Direction, Kind: spec.Kind, Pod: spec.Pod, Container: spec.Container,
		RemotePath: spec.RemotePath, Size: spec.Size, Offset: spec.Offset, Checksum: spec.Checksum,
		Overwrite: spec.Overwrite, ResumeID: spec.ResumeID, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, ExpiresAt: expiresAt,
	}, nil
}

func owned(task storage.Task, principal controlplaneapi.Principal, session sessionapi.ActiveSession) bool {
	return task.Type == TaskType && task.PrincipalID == principal.Subject && task.SessionID == session.ID
}

func normalizeRoots(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{"/"}, nil
	}
	roots := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
		if value == "/" {
			roots = append(roots, value)
			continue
		}
		root, err := normalizeRemotePath(value, []string{"/"})
		if err != nil {
			return nil, fmt.Errorf("file transfer allowed root is invalid: %w", err)
		}
		roots = append(roots, root)
	}
	slices.Sort(roots)
	return slices.Compact(roots), nil
}

func normalizeRemotePath(value string, allowedRoots []string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || len(value) > 4096 || !path.IsAbs(value) {
		return "", errors.New("container path must be an absolute path of at most 4096 bytes")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return "", errors.New("container path contains control characters")
		}
	}
	for component := range strings.SplitSeq(value, "/") {
		if component == "." || component == ".." {
			return "", errors.New("container path traversal is not allowed")
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "/" {
		return "", errors.New("container root cannot be transferred")
	}
	if matchingAllowedRoot(cleaned, allowedRoots) != "" {
		return cleaned, nil
	}
	return "", errors.New("container path is outside the configured allowed roots")
}

func matchingAllowedRoot(value string, allowedRoots []string) string {
	matched := ""
	for _, root := range allowedRoots {
		if (root == "/" || value == root || strings.HasPrefix(value, root+"/")) && len(root) > len(matched) {
			matched = root
		}
	}
	return matched
}

// NormalizeAllowedRoots validates and canonicalizes the container roots shared
// by transfer and directory-management APIs.
func NormalizeAllowedRoots(values []string) ([]string, error) { return normalizeRoots(values) }

// NormalizeContainerPath applies the common lexical path policy and returns
// the most-specific configured root that contains the path.
func NormalizeContainerPath(value string, allowedRoots []string) (string, string, error) {
	if strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")) == "/" && slices.Contains(allowedRoots, "/") {
		return "/", "/", nil
	}
	normalized, err := normalizeRemotePath(value, allowedRoots)
	if err != nil {
		return "", "", err
	}
	root := matchingAllowedRoot(normalized, allowedRoots)
	if root == "" {
		return "", "", errors.New("container path is outside the configured allowed roots")
	}
	return normalized, root, nil
}

func namespaceFromQuery(request *http.Request) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["namespace"]) != 1 || len(validation.IsDNS1123Label(query.Get("namespace"))) != 0 {
		return "", invalid("namespace", "one valid namespace query parameter is required")
	}
	return query.Get("namespace"), nil
}

func targetError(err error) *controlplaneapi.Error {
	switch {
	case apierrors.IsForbidden(err):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeForbidden, Message: "Kubernetes file access is not permitted", Cause: err}
	case apierrors.IsNotFound(err):
		return notFound()
	default:
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Message: "file transfer target is unavailable", Cause: err}
	}
}

func storageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict), errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "file transfer Task conflicts with an existing request", Cause: err}
	default:
		return internalError(err)
	}
}

func invalid(field, message string) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: field, Message: message}
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "file transfer operation failed", Cause: err}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
}

func writeJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}
