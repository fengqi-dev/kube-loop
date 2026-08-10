package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
)

const (
	APIPathPrefix   = "/api/v2"
	RequestIDHeader = "X-Request-ID"
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

type APIError struct {
	Code    ErrorCode
	Message string
	Field   string
	Cause   error
}

func (apiError *APIError) Error() string {
	if apiError == nil {
		return ""
	}
	return apiError.Message
}

type errorEnvelope struct {
	Error errorDocument `json:"error"`
}

type errorDocument struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Field     string    `json:"field,omitempty"`
	RequestID string    `json:"requestId"`
}

type Principal struct {
	Subject         string
	Provider        string
	DisplayName     string
	Email           string
	Groups          []string
	DeviceID        string
	FamilyID        string
	AccessExpiresAt time.Time
}

type Authenticator interface {
	Authenticate(*http.Request) (Principal, *APIError)
}

type AuthenticatorFunc func(*http.Request) (Principal, *APIError)

func (function AuthenticatorFunc) Authenticate(request *http.Request) (Principal, *APIError) {
	return function(request)
}

type APIHandler interface {
	ServeAPI(http.ResponseWriter, *http.Request, Principal) *APIError
}

type APIHandlerFunc func(http.ResponseWriter, *http.Request, Principal) *APIError

func (function APIHandlerFunc) ServeAPI(writer http.ResponseWriter, request *http.Request, principal Principal) *APIError {
	return function(writer, request, principal)
}

type serverOptions struct {
	authenticator     Authenticator
	authorizer        authorization.Authorizer
	audit             AuditSink
	apiHandler        APIHandler
	readiness         ReadinessChecker
	authHandler       http.Handler
	managementHandler http.Handler
}

type ServerOption func(*serverOptions)

func WithAuthenticator(authenticator Authenticator) ServerOption {
	return func(options *serverOptions) { options.authenticator = authenticator }
}

func WithAuthorizer(authorizer authorization.Authorizer) ServerOption {
	return func(options *serverOptions) { options.authorizer = authorizer }
}

func WithAuditSink(sink AuditSink) ServerOption {
	return func(options *serverOptions) { options.audit = sink }
}

func WithAPIHandler(handler APIHandler) ServerOption {
	return func(options *serverOptions) { options.apiHandler = handler }
}

type ReadinessChecker interface {
	Check(context.Context) error
}

type ReadinessCheckFunc func(context.Context) error

func (function ReadinessCheckFunc) Check(ctx context.Context) error {
	return function(ctx)
}

func WithReadinessChecker(checker ReadinessChecker) ServerOption {
	return func(options *serverOptions) { options.readiness = checker }
}

func WithAuthHandler(handler http.Handler) ServerOption {
	return func(options *serverOptions) { options.authHandler = handler }
}

// WithManagementHandler installs the browser-only Management Plane below
// /api/v2/admin without passing it through the ordinary Gateway Bearer chain.
func WithManagementHandler(handler http.Handler) ServerOption {
	return func(options *serverOptions) { options.managementHandler = handler }
}

func newAPIFramework(config Config, logger *slog.Logger, options serverOptions) http.Handler {
	if options.authenticator == nil {
		options.authenticator = AuthenticatorFunc(func(*http.Request) (Principal, *APIError) {
			return Principal{}, &APIError{Code: CodeUnauthenticated, Message: "authentication required"}
		})
	}
	if options.apiHandler == nil {
		options.apiHandler = APIHandlerFunc(func(http.ResponseWriter, *http.Request, Principal) *APIError {
			return &APIError{Code: CodeNotFound, Message: "resource not found"}
		})
	}
	if options.authorizer == nil {
		options.authorizer = authorization.NewDenyAll()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		trackedWriter := &responseStateWriter{ResponseWriter: writer}
		requestID := newRequestID()
		authorizationRequest := authorizationRequestForHTTP(request)
		var principal Principal
		trackedWriter.Header().Set(RequestIDHeader, requestID)
		trackedWriter.Header().Set("Cache-Control", "no-store")
		requestContext := request.Context()
		cancel := func() {}
		if !isWebSocketUpgrade(request) {
			requestContext, cancel = context.WithTimeout(request.Context(), config.APIRequestTimeout)
		}
		defer cancel()
		requestContext = storage.WithAuditRequestID(requestContext, requestID)
		request = request.WithContext(context.WithValue(requestContext, requestIDContextKey{}, requestID))
		auditState := &auditContextState{}
		request = request.WithContext(context.WithValue(request.Context(), auditContextKey{}, auditState))
		request.Body = http.MaxBytesReader(trackedWriter, request.Body, config.MaxRequestBodyBytes)
		defer func() {
			if options.audit == nil {
				return
			}
			status := trackedWriter.status
			if status == 0 {
				status = http.StatusOK
			}
			record := AuditRecord{
				RequestID: requestID, PrincipalID: principal.Subject, SessionID: auditState.SessionID,
				Operation: authorizationRequest.Operation, Namespace: authorizationRequest.Namespace,
				ResourceKind: authorizationRequest.ResourceKind, ResourceName: authorizationRequest.ResourceName,
				Outcome: auditOutcome(status), HTTPStatus: status, Duration: time.Since(startedAt),
			}
			if _, decision, ok := AuthorizationFromContext(request.Context()); ok {
				record.PolicyRuleID = decision.RuleID
			}
			if err := options.audit.Record(request.Context(), record); err != nil {
				logger.ErrorContext(request.Context(), "append API audit event failed", "request_id", requestID)
			}
		}()
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(request.Context(), "panic in API handler", "request_id", requestID)
				if !trackedWriter.wroteHeader {
					writeAPIError(trackedWriter, requestID, &APIError{Code: CodeInternal, Message: "internal server error"})
				}
			}
		}()
		var authenticationError *APIError
		principal, authenticationError = options.authenticator.Authenticate(request)
		if authenticationError != nil {
			if authenticationError.Code == CodeUnauthenticated {
				trackedWriter.Header().Set("WWW-Authenticate", "Bearer")
			}
			writeAPIError(trackedWriter, requestID, authenticationError)
			return
		}
		if principal.Subject == "" {
			writeAPIError(trackedWriter, requestID, &APIError{Code: CodeUnauthenticated, Message: "authentication required"})
			return
		}
		decision := options.authorizer.Authorize(request.Context(), authorization.Subject{
			ID: principal.Subject, Groups: append([]string(nil), principal.Groups...),
		}, authorizationRequest)
		if !decision.Allowed {
			writeAPIError(trackedWriter, requestID, &APIError{Code: CodeForbidden, Message: "operation is not permitted"})
			return
		}
		request = request.WithContext(context.WithValue(request.Context(), authorizationContextKey{}, authorizationContextValue{
			Request: authorizationRequest, Decision: decision,
		}))
		if apiError := options.apiHandler.ServeAPI(trackedWriter, request, principal); apiError != nil && !trackedWriter.wroteHeader {
			writeAPIError(trackedWriter, requestID, apiError)
		}
	})
}

type authorizationContextKey struct{}

type authorizationContextValue struct {
	Request  authorization.Request
	Decision authorization.Decision
}

func AuthorizationFromContext(ctx context.Context) (authorization.Request, authorization.Decision, bool) {
	value, ok := ctx.Value(authorizationContextKey{}).(authorizationContextValue)
	return value.Request, value.Decision, ok
}

// RequireAuthorizedRequest is the feature-dispatch boundary. The top-level API
// framework is the only component that may create this authorization proof
// after invoking the configured Authorizer. Routers and feature handlers must
// reject direct dispatch when that proof is absent, malformed, or denied.
func RequireAuthorizedRequest(ctx context.Context) *APIError {
	request, decision, ok := AuthorizationFromContext(ctx)
	if !ok || !decision.Allowed || strings.TrimSpace(request.Operation) == "" ||
		strings.TrimSpace(request.ResourceKind) == "" {
		return &APIError{Code: CodeForbidden, Message: "operation is not permitted"}
	}
	return nil
}

func authorizationRequestForHTTP(request *http.Request) authorization.Request {
	path := strings.TrimPrefix(request.URL.Path, APIPathPrefix)
	parts := strings.FieldsFunc(path, func(character rune) bool { return character == '/' })
	result := authorization.Request{Namespace: strings.TrimSpace(request.URL.Query().Get("namespace"))}
	if len(parts) == 0 {
		result.ResourceKind = "api"
	} else if parts[0] == "namespaces" {
		result.ResourceKind = "namespaces"
		if len(parts) == 2 {
			result.ResourceName = parts[1]
		} else if len(parts) >= 3 {
			result.Namespace = parts[1]
			result.ResourceKind = strings.ToLower(parts[2])
			if len(parts) >= 4 {
				result.ResourceName = parts[3]
			}
		}
	} else {
		result.ResourceKind = strings.ToLower(parts[0])
		if len(parts) >= 2 {
			result.ResourceName = parts[1]
		}
	}
	switch request.Method {
	case http.MethodGet:
		if request.URL.Query().Get("watch") == "true" {
			result.Operation = "watch"
		} else if result.ResourceName != "" {
			result.Operation = "get"
		} else {
			result.Operation = "list"
		}
	case http.MethodPost:
		result.Operation = "create"
	case http.MethodPut:
		result.Operation = "update"
	case http.MethodPatch:
		result.Operation = "patch"
	case http.MethodDelete:
		result.Operation = "delete"
	default:
		result.Operation = strings.ToLower(request.Method)
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "heartbeat" {
		result.Operation = "heartbeat"
	}
	if len(parts) == 3 && parts[0] == "sessions" && parts[2] == "tickets" {
		result.ResourceKind = "relay-tickets"
		result.ResourceName = parts[1]
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "port-forwards" {
		result.ResourceKind = "port-forwards"
		result.ResourceName = parts[1]
		if len(parts) == 3 && request.Method == http.MethodGet {
			result.Operation = "list"
		}
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "exec" {
		result.ResourceKind = "pod-exec"
		result.ResourceName = parts[1]
		if len(parts) == 5 && parts[4] == "stream" && request.Method == http.MethodGet {
			result.Operation = "stream"
			result.ResourceName = parts[3]
		}
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "file-transfers" {
		result.ResourceKind = "file-transfers"
		result.ResourceName = parts[1]
		if len(parts) >= 4 {
			result.ResourceName = parts[3]
		}
		if len(parts) == 5 && parts[4] == "stream" && request.Method == http.MethodGet {
			result.Operation = "stream"
		}
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "exchanges" {
		result.ResourceKind = "exchanges"
		result.ResourceName = parts[1]
		if len(parts) >= 4 {
			result.ResourceName = parts[3]
		}
		if len(parts) == 5 && parts[4] == "stream" && request.Method == http.MethodGet {
			result.Operation = "stream"
		}
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "mirrors" {
		result.ResourceKind = "mirrors"
		result.ResourceName = parts[1]
		if len(parts) >= 4 {
			result.ResourceName = parts[3]
		}
		if len(parts) == 5 && parts[4] == "stream" && request.Method == http.MethodGet {
			result.Operation = "stream"
		}
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "previews" {
		result.ResourceKind = "previews"
		result.ResourceName = parts[1]
		if len(parts) >= 4 {
			result.ResourceName = parts[3]
		}
		if len(parts) == 5 && parts[4] == "stream" && request.Method == http.MethodGet {
			result.Operation = "stream"
		}
	}
	if len(parts) >= 4 && parts[0] == "sessions" && parts[2] == "pod-files" {
		result.ResourceKind = "pod-files"
		result.ResourceName = parts[1]
		switch parts[3] {
		case "list":
			result.Operation = "list"
		case "create":
			result.Operation = "create"
		case "rename":
			result.Operation = "update"
		case "delete":
			result.Operation = "delete"
		case "operations":
			result.Operation = "get"
			if len(parts) >= 5 {
				result.ResourceName = parts[4]
			}
		}
	}
	return result
}

type responseStateWriter struct {
	http.ResponseWriter
	wroteHeader bool
	status      int
}

// Unwrap lets http.ResponseController reach optional interfaces such as
// Hijacker without bypassing status tracking and audit middleware.
func (writer *responseStateWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *responseStateWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *responseStateWriter) Write(content []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(content)
}

func isWebSocketUpgrade(request *http.Request) bool {
	if request.Method != http.MethodGet || !strings.EqualFold(strings.TrimSpace(request.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for value := range strings.SplitSeq(request.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(value), "upgrade") {
			return true
		}
	}
	return false
}

type requestIDContextKey struct{}

type auditContextKey struct{}

type auditContextState struct {
	SessionID string
}

func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func SetAuditSessionID(ctx context.Context, sessionID string) {
	if state, ok := ctx.Value(auditContextKey{}).(*auditContextState); ok && state != nil {
		state.SessionID = strings.TrimSpace(sessionID)
	}
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

func writeAPIError(writer http.ResponseWriter, requestID string, apiError *APIError) {
	if apiError == nil {
		apiError = &APIError{Code: CodeInternal, Message: "internal server error"}
	}
	if apiError.Code == "" {
		apiError.Code = CodeInternal
	}
	if apiError.Message == "" {
		apiError.Message = defaultErrorMessage(apiError.Code)
	}
	writeJSON(writer, statusForError(apiError.Code), errorEnvelope{Error: errorDocument{
		Code: apiError.Code, Message: apiError.Message, Field: apiError.Field, RequestID: requestID,
	}})
}

func defaultErrorMessage(code ErrorCode) string {
	switch code {
	case CodeUnauthenticated:
		return "authentication required"
	case CodeForbidden:
		return "operation forbidden"
	case CodeNotFound:
		return "resource not found"
	case CodeConflict:
		return "resource conflict"
	case CodeInvalidArgument:
		return "invalid argument"
	case CodeUnavailable:
		return "service unavailable"
	case CodeVersionMismatch:
		return "client version is not supported"
	case CodeRateLimited:
		return "rate limit exceeded"
	default:
		return "internal server error"
	}
}

func statusForError(code ErrorCode) int {
	switch code {
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeInvalidArgument:
		return http.StatusBadRequest
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeVersionMismatch:
		return http.StatusUpgradeRequired
	case CodeRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

func DecodeJSON(request *http.Request, destination any) *APIError {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return &APIError{Code: CodeInvalidArgument, Message: "Content-Type must be application/json"}
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return &APIError{Code: CodeInvalidArgument, Message: "request body exceeds the size limit"}
		}
		if errors.Is(err, io.EOF) {
			return &APIError{Code: CodeInvalidArgument, Message: "request body is required"}
		}
		return &APIError{Code: CodeInvalidArgument, Message: "request body contains invalid JSON"}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &APIError{Code: CodeInvalidArgument, Message: "request body must contain one JSON document"}
	}
	return nil
}

func IsAPIPath(path string) bool {
	return path == APIPathPrefix || strings.HasPrefix(path, APIPathPrefix+"/")
}
