package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type ErrorCode string

const (
	ErrorInvalidArgument ErrorCode = "invalid_argument"
	ErrorUnauthenticated ErrorCode = "unauthenticated"
	ErrorForbidden       ErrorCode = "forbidden"
	ErrorNotFound        ErrorCode = "not_found"
	ErrorConflict        ErrorCode = "conflict"
	ErrorUnavailable     ErrorCode = "unavailable"
	ErrorInternal        ErrorCode = "internal"
)

// ToolError has a stable, machine-readable JSON Error string because the MCP
// SDK places returned errors into a tool result's text content.
type ToolError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Field     string    `json:"field,omitempty"`
	RequestID string    `json:"requestId,omitempty"`
	cause     error
}

func (toolError *ToolError) Error() string {
	if toolError == nil {
		return ""
	}
	raw, _ := json.Marshal(struct {
		Code      ErrorCode `json:"code"`
		Message   string    `json:"message"`
		Field     string    `json:"field,omitempty"`
		RequestID string    `json:"requestId,omitempty"`
	}{toolError.Code, toolError.Message, toolError.Field, toolError.RequestID})
	return string(raw)
}

func (toolError *ToolError) Unwrap() error { return toolError.cause }

func invalid(field, message string) error {
	return &ToolError{Code: ErrorInvalidArgument, Message: message, Field: field}
}

func stableError(err error) error {
	if err == nil {
		return nil
	}
	var existing *ToolError
	if errors.As(err, &existing) {
		return existing
	}
	var apiError *clientremote.APIError
	if errors.As(err, &apiError) {
		code := ErrorInternal
		switch apiError.Status {
		case 400, 422:
			code = ErrorInvalidArgument
		case 401:
			code = ErrorUnauthenticated
		case 403:
			code = ErrorForbidden
		case 404:
			code = ErrorNotFound
		case 409:
			code = ErrorConflict
		case 429, 502, 503, 504:
			code = ErrorUnavailable
		}
		message := strings.TrimSpace(apiError.Message)
		if message == "" {
			message = "Control Plane request failed"
		}
		return &ToolError{
			Code: code, Message: message, Field: apiError.Field,
			RequestID: apiError.RequestID, cause: err,
		}
	}
	if errors.Is(err, credentials.ErrNotFound) {
		return &ToolError{Code: ErrorUnauthenticated, Message: "sign in to the active Server Profile", cause: err}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &ToolError{Code: ErrorUnavailable, Message: "operation was cancelled or timed out", cause: err}
	}
	var networkError net.Error
	if errors.As(err, &networkError) || strings.Contains(err.Error(), "Gateway request") ||
		strings.Contains(err.Error(), "Control Plane request") {
		return &ToolError{Code: ErrorUnavailable, Message: "Control Plane is unavailable", cause: err}
	}
	return &ToolError{Code: ErrorInternal, Message: "operation failed", cause: err}
}
