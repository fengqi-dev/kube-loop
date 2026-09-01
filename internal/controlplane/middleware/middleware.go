package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

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
		config.Authenticator = controlplaneapi.AuthenticatorFunc(
			func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
				return controlplaneapi.Identity{}, &controlplaneapi.Error{
					Code:    controlplaneapi.CodeUnauthenticated,
					Message: authenticationRequiredMessage,
				}
			},
		)
	}
	if config.Authorizer == nil {
		config.Authorizer = authorization.NewAuthenticated()
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
			requestID := strings.TrimSpace(
				response.Header().Get(echo.HeaderXRequestID),
			)
			requestContext := storage.WithAuditRequestID(
				request.Context(),
				requestID,
			)
			requestContext = context.WithValue(
				requestContext,
				requestIDContextKey{},
				requestID,
			)
			request = request.WithContext(requestContext)
			ctx.SetRequest(request)
			authorizationRequest := authorizationRequestForHTTP(
				request,
				config.APIPathPrefix,
			)
			var identity controlplaneapi.Identity
			response.Header().Set("Cache-Control", "no-store")
			cancel := func() {}
			if !isWebSocketUpgrade(request) {
				requestContext, cancel = context.WithTimeout(
					requestContext,
					config.RequestTimeout,
				)
			}
			defer cancel()
			auditState := &auditContextState{}
			requestContext = context.WithValue(
				requestContext,
				auditContextKey{},
				auditState,
			)
			request = request.WithContext(requestContext)
			request.Body = http.MaxBytesReader(
				response,
				request.Body,
				config.MaxRequestBodySize,
			)
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
						writeError(
							ctx,
							requestID,
							&controlplaneapi.Error{
								Code:    controlplaneapi.CodeInternal,
								Message: internalServerErrorMessage,
							},
						)
					}
					returnedError = nil
				}
				status := responseState.Status
				if status == 0 {
					status = http.StatusOK
				}
				recordAudit(
					request.Context(), config.Audit, config.Logger, requestID, identity,
					auditState, authorizationRequest, status, time.Since(startedAt),
				)
			}()

			var authenticationError *controlplaneapi.Error
			identity, authenticationError = config.Authenticator.Authenticate(
				request,
			)
			if authenticationError != nil {
				if authenticationError.Code == controlplaneapi.CodeUnauthenticated {
					response.Header().Set("WWW-Authenticate", "Bearer")
				}
				writeError(ctx, requestID, authenticationError)
				return nil
			}
			decision, authorizationError := authorizeIdentity(
				request.Context(), config.Authorizer, identity, authorizationRequest,
			)
			if authorizationError != nil {
				writeError(ctx, requestID, authorizationError)
				return nil
			}
			requestContext = request.Context()
			requestContext = context.WithValue(
				requestContext,
				authorizationContextKey{},
				authorizationContextValue{
					request: authorizationRequest, decision: decision,
				},
			)
			requestContext = context.WithValue(
				requestContext,
				identityContextKey{},
				identity,
			)
			request = request.WithContext(requestContext)
			ctx.SetRequest(request)

			returnedError = next(ctx)
			if returnedError == nil || responseState.Committed {
				return returnedError
			}
			var apiError *controlplaneapi.Error
			switch {
			case errors.As(returnedError, &apiError):
			case errors.Is(returnedError, echo.ErrNotFound),
				errors.Is(returnedError, echo.ErrMethodNotAllowed):
				apiError = &controlplaneapi.Error{
					Code:    controlplaneapi.CodeNotFound,
					Message: "resource not found",
				}
			default:
				apiError = &controlplaneapi.Error{
					Code:    controlplaneapi.CodeInternal,
					Message: internalServerErrorMessage,
					Cause:   returnedError,
				}
			}
			if apiError.Cause != nil && apiError.Code == controlplaneapi.CodeInternal {
				config.Logger.ErrorContext(
					request.Context(),
					"API handler failed",
					"request_id", requestID,
					"error", apiError.Cause,
				)
			}
			writeError(ctx, requestID, apiError)
			return nil
		}
	}
}

func isWebSocketUpgrade(request *http.Request) bool {
	if request.Method != http.MethodGet ||
		!strings.EqualFold(
			strings.TrimSpace(request.Header.Get("Upgrade")),
			"websocket",
		) {
		return false
	}
	for value := range strings.SplitSeq(request.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(value), "upgrade") {
			return true
		}
	}
	return false
}
