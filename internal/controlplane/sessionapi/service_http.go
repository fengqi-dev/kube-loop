package sessionapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

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
			return "", &controlplaneapi.Error{
				Code:    controlplaneapi.CodeInvalidArgument,
				Field:   key,
				Message: "query parameter is not supported",
			}
		}
		if len(values) != 1 {
			return "", &controlplaneapi.Error{
				Code:    controlplaneapi.CodeInvalidArgument,
				Field:   key,
				Message: "query parameter must be provided once",
			}
		}
	}
	namespace := query.Get("namespace")
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return "", &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "namespace",
			Message: "namespace is invalid",
		}
	}
	return namespace, nil
}

func idempotencyKey(request *http.Request) (string, *controlplaneapi.Error) {
	values := request.Header.Values(IdempotencyHeader)
	if len(values) != 1 {
		return "", &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   IdempotencyHeader,
			Message: "Idempotency-Key must be provided once",
		}
	}
	key := strings.TrimSpace(values[0])
	if key == "" || len(key) > 128 {
		return "", &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   IdempotencyHeader,
			Message: "Idempotency-Key is invalid",
		}
	}
	for _, character := range key {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._:", character) {
			continue
		}
		return "", &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   IdempotencyHeader,
			Message: "Idempotency-Key is invalid",
		}
	}
	return key, nil
}

func expectedGeneration(request *http.Request) (uint64, *controlplaneapi.Error) {
	values := request.Header.Values(ifMatchHeader)
	if len(values) != 1 {
		return 0, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   ifMatchHeader,
			Message: "If-Match generation is required",
		}
	}
	raw := strings.TrimSpace(values[0])
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return 0, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   ifMatchHeader,
			Message: "If-Match generation is invalid",
		}
	}
	generation, err := strconv.ParseUint(raw[1:len(raw)-1], 10, 64)
	if err != nil || generation == 0 {
		return 0, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   ifMatchHeader,
			Message: "If-Match generation is invalid",
		}
	}
	return generation, nil
}

func requireEmptyBody(request *http.Request) *controlplaneapi.Error {
	contents, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Message: "request body is invalid",
		}
	}
	if len(contents) != 0 {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Message: "request body must be empty",
		}
	}
	return nil
}

func mapStorageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Session state changed; reload and retry",
			Cause:   err,
		}
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Idempotency-Key was already used for a different request",
			Cause:   err,
		}
	default:
		return internalError(err)
	}
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInternal,
		Message: "Session operation failed",
		Cause:   err,
	}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
}
