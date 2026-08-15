package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/labstack/echo/v5"
)

type AuditRecord struct {
	RequestID    string
	IdentityID   string
	SessionID    string
	Operation    string
	Namespace    string
	ResourceKind string
	ResourceName string
	Outcome      string
	PolicyRuleID string
	HTTPStatus   int
	Duration     time.Duration
}

type AuditSink interface {
	Record(context.Context, AuditRecord) error
}

type Config struct {
	APIPathPrefix      string
	RequestTimeout     time.Duration
	MaxRequestBodySize int64
	Logger             *slog.Logger
	Authenticator      controlplaneapi.Authenticator
	Authorizer         authorization.Authorizer
	Audit              AuditSink
}

func New(config Config) echo.MiddlewareFunc {
	if config.Authenticator == nil {
		config.Authenticator = controlplaneapi.AuthenticatorFunc(func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
			return controlplaneapi.Identity{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required"}
		})
	}
	if config.Authorizer == nil {
		config.Authorizer = authorization.NewDenyAll()
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx *echo.Context) (returnedError error) {
			startedAt := time.Now()
			response := ctx.Response()
			responseState, err := echo.UnwrapResponse(response)
			if err != nil {
				return err
			}
			request := ctx.Request()
			requestID := strings.TrimSpace(response.Header().Get(echo.HeaderXRequestID))
			requestContext := storage.WithAuditRequestID(request.Context(), requestID)
			requestContext = context.WithValue(requestContext, requestIDContextKey{}, requestID)
			request = request.WithContext(requestContext)
			ctx.SetRequest(request)
			authorizationRequest := authorizationRequestForHTTP(request, config.APIPathPrefix)
			var identity controlplaneapi.Identity
			response.Header().Set("Cache-Control", "no-store")
			cancel := func() {}
			if !isWebSocketUpgrade(request) {
				requestContext, cancel = context.WithTimeout(requestContext, config.RequestTimeout)
			}
			defer cancel()
			auditState := &auditContextState{}
			requestContext = context.WithValue(requestContext, auditContextKey{}, auditState)
			request = request.WithContext(requestContext)
			request.Body = http.MaxBytesReader(response, request.Body, config.MaxRequestBodySize)
			ctx.SetRequest(request)

			defer func() {
				if recovered := recover(); recovered != nil {
					config.Logger.ErrorContext(
						request.Context(),
						"panic in API handler",
						"request_id", requestID,
						"error", recovered,
						"stack", string(debug.Stack()),
					)
					if !responseState.Committed {
						writeError(ctx, requestID, &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "internal server error"})
					}
					returnedError = nil
				}
				if config.Audit == nil {
					return
				}
				status := responseState.Status
				if status == 0 {
					status = http.StatusOK
				}
				record := AuditRecord{
					RequestID: requestID, IdentityID: identity.Subject, SessionID: auditState.sessionID,
					Operation: authorizationRequest.Operation, Namespace: authorizationRequest.Namespace,
					ResourceKind: authorizationRequest.ResourceKind, ResourceName: authorizationRequest.ResourceName,
					Outcome: auditOutcome(status), HTTPStatus: status, Duration: time.Since(startedAt),
				}
				if _, decision, ok := AuthorizationFromContext(request.Context()); ok {
					record.PolicyRuleID = decision.RuleID
				}
				if err := config.Audit.Record(request.Context(), record); err != nil {
					config.Logger.ErrorContext(request.Context(), "append API audit event failed", "request_id", requestID)
				}
			}()

			var authenticationError *controlplaneapi.Error
			identity, authenticationError = config.Authenticator.Authenticate(request)
			if authenticationError != nil {
				if authenticationError.Code == controlplaneapi.CodeUnauthenticated {
					response.Header().Set("WWW-Authenticate", "Bearer")
				}
				writeError(ctx, requestID, authenticationError)
				return nil
			}
			if identity.Subject == "" {
				writeError(ctx, requestID, &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required"})
				return nil
			}
			requestContext = request.Context()
			if !isNamespaceCollectionList(authorizationRequest) && !isAuthenticatedMetadataRead(authorizationRequest) && !isSessionHeartbeat(authorizationRequest) {
				decision := config.Authorizer.Authorize(request.Context(), authorization.Subject{
					ID: identity.Subject, Provider: identity.Provider, Groups: append([]string(nil), identity.Groups...),
				}, authorizationRequest)
				if !decision.Allowed {
					writeError(ctx, requestID, &controlplaneapi.Error{Code: controlplaneapi.CodeForbidden, Message: "operation is not permitted"})
					return nil
				}
				requestContext = context.WithValue(requestContext, authorizationContextKey{}, authorizationContextValue{
					request: authorizationRequest, decision: decision,
				})
			}
			requestContext = context.WithValue(requestContext, identityContextKey{}, identity)
			request = request.WithContext(requestContext)
			ctx.SetRequest(request)

			returnedError = next(ctx)
			if returnedError == nil || responseState.Committed {
				return returnedError
			}
			var apiError *controlplaneapi.Error
			switch {
			case errors.As(returnedError, &apiError):
			case errors.Is(returnedError, echo.ErrNotFound), errors.Is(returnedError, echo.ErrMethodNotAllowed):
				apiError = &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
			default:
				apiError = &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "internal server error", Cause: returnedError}
			}
			writeError(ctx, requestID, apiError)
			return nil
		}
	}
}

// The namespace collection is authorized per returned namespace by kubeapi.
// A cluster-scoped pre-check here would reject identities that only have
// namespace-scoped grants before the response can be filtered.
func isNamespaceCollectionList(request authorization.Request) bool {
	return request.Operation == "list" && request.Namespace == "" &&
		request.ResourceKind == "namespaces" && request.ResourceName == ""
}

// Version discovery is required before the client has selected a Namespace.
// Authentication still applies, while namespace authorization is enforced by
// the subsequent capability and inventory requests.
func isAuthenticatedMetadataRead(request authorization.Request) bool {
	return request.Operation == "list" && request.Namespace == "" &&
		request.ResourceKind == "version" && request.ResourceName == ""
}

// Session heartbeats re-evaluate the complete policy and Kubernetes capability
// intersection inside sessionapi. Keeping this request out of the early gate
// lets that service actively disconnect the runtime and settle its Tasks when
// access has been revoked, instead of merely returning 403 while traffic lives on.
func isSessionHeartbeat(request authorization.Request) bool {
	return request.Operation == "heartbeat" && request.ResourceKind == "sessions" &&
		request.Namespace != "" && request.ResourceName != ""
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

func auditOutcome(status int) string {
	switch {
	case status == 0 || status >= 200 && status < 300:
		return "success"
	case status == http.StatusUnauthorized:
		return "unauthenticated"
	case status == http.StatusForbidden:
		return "denied"
	default:
		return "error"
	}
}
