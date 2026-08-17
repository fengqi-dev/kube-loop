package controlplaneapi

import (
	"net/http"
	"time"
)

type ErrorCode string

const (
	CodeUnauthenticated ErrorCode = "UNAUTHENTICATED"
	CodeForbidden       ErrorCode = "FORBIDDEN"
	CodeNotFound        ErrorCode = "NOT_FOUND"
	CodeConflict        ErrorCode = "CONFLICT"
	CodeInvalidArgument ErrorCode = "INVALID_ARGUMENT"
	CodeUnavailable     ErrorCode = "UNAVAILABLE"
	CodeVersionMismatch ErrorCode = "VERSION_MISMATCH"
	CodeRateLimited     ErrorCode = "RATE_LIMITED"
	CodeInternal        ErrorCode = "INTERNAL"
)

type Error struct {
	Code    ErrorCode
	Message string
	Field   string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type Identity struct {
	Subject         string
	Provider        string
	DisplayName     string
	Email           string
	Groups          []string
	DeviceID        string
	AuthorizationID string
	AccessExpiresAt time.Time
}

type Authenticator interface {
	Authenticate(*http.Request) (Identity, *Error)
}

type AuthenticatorFunc func(*http.Request) (Identity, *Error)

func (f AuthenticatorFunc) Authenticate(request *http.Request) (Identity, *Error) {
	return f(request)
}
