package middleware

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func BindingError(err error) *controlplaneapi.Error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Message: "request body exceeds the size limit",
			Cause:   err,
		}
	}
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInvalidArgument,
		Message: "request binding failed",
		Cause:   err,
	}
}

type errorEnvelope struct {
	Error errorDocument `json:"error"`
}

type errorDocument struct {
	Code      controlplaneapi.ErrorCode `json:"code"`
	Message   string                    `json:"message"`
	Field     string                    `json:"field,omitempty"`
	RequestID string                    `json:"requestId"`
}

func writeError(
	ctx *echo.Context,
	requestID string,
	apiError *controlplaneapi.Error,
) {
	if apiError == nil {
		apiError = &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInternal,
			Message: internalServerErrorMessage,
		}
	}
	if apiError.Code == "" {
		apiError.Code = controlplaneapi.CodeInternal
	}
	if apiError.Message == "" {
		apiError.Message = defaultErrorMessage(apiError.Code)
	}
	_ = ctx.JSON(
		statusForError(apiError.Code),
		errorEnvelope{Error: errorDocument{
			Code: apiError.Code, Message: apiError.Message, Field: apiError.Field, RequestID: requestID,
		}},
	)
}

func defaultErrorMessage(code controlplaneapi.ErrorCode) string {
	switch code {
	case controlplaneapi.CodeUnauthenticated:
		return authenticationRequiredMessage
	case controlplaneapi.CodeForbidden:
		return "operation forbidden"
	case controlplaneapi.CodeNotFound:
		return "resource not found"
	case controlplaneapi.CodeConflict:
		return "resource conflict"
	case controlplaneapi.CodeInvalidArgument:
		return "invalid argument"
	case controlplaneapi.CodeUnavailable:
		return "service unavailable"
	case controlplaneapi.CodeVersionMismatch:
		return "client version is not supported"
	case controlplaneapi.CodeRateLimited:
		return "rate limit exceeded"
	case controlplaneapi.CodeInternal:
		return internalServerErrorMessage
	default:
		return internalServerErrorMessage
	}
}

func statusForError(code controlplaneapi.ErrorCode) int {
	switch code {
	case controlplaneapi.CodeUnauthenticated:
		return http.StatusUnauthorized
	case controlplaneapi.CodeForbidden:
		return http.StatusForbidden
	case controlplaneapi.CodeNotFound:
		return http.StatusNotFound
	case controlplaneapi.CodeConflict:
		return http.StatusConflict
	case controlplaneapi.CodeInvalidArgument:
		return http.StatusBadRequest
	case controlplaneapi.CodeUnavailable:
		return http.StatusServiceUnavailable
	case controlplaneapi.CodeVersionMismatch:
		return http.StatusUpgradeRequired
	case controlplaneapi.CodeRateLimited:
		return http.StatusTooManyRequests
	case controlplaneapi.CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}
