package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	adminbootstrap "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/bootstrap"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

type Option func(*handlerOptions) error

type handlerOptions struct {
	readAPI            *readAPI
	tokenAuthenticator TokenAuthenticator
	localUsers         *adminlocaluser.Service
	bootstrapService   *adminbootstrap.Service
	oauthRepositories  storage.Repositories
	oauthTransactions  storage.TransactionManager
}

func WithOAuthClients(repositories storage.Repositories, transactions storage.TransactionManager) Option {
	return func(options *handlerOptions) error {
		if repositories == nil || transactions == nil {
			return errors.New("OAuth client API repositories are required")
		}
		options.oauthRepositories, options.oauthTransactions = repositories, transactions
		return nil
	}
}

func WithLocalUsers(service *adminlocaluser.Service) Option {
	return func(options *handlerOptions) error {
		if service == nil {
			return errors.New("management local user service is required")
		}
		if options.localUsers != nil {
			return errors.New("management local user service is already configured")
		}
		options.localUsers = service
		return nil
	}
}

func WithReadAPI(repositories storage.Repositories) Option {
	return func(options *handlerOptions) error {
		if repositories == nil {
			return errors.New("management API repositories are required")
		}
		if options.readAPI != nil {
			return errors.New("management read API is already configured")
		}
		options.readAPI = &readAPI{repositories: repositories}
		return nil
	}
}

type readAPI struct {
	handler           *Handler
	repositories      storage.Repositories
	localUsers        *adminlocaluser.Service
	bootstrapService  *adminbootstrap.Service
	oauthRepositories storage.Repositories
	oauthTransactions storage.TransactionManager
}

func WithBootstrap(service *adminbootstrap.Service) Option {
	return func(options *handlerOptions) error {
		if service == nil {
			return errors.New("IAM bootstrap service is required")
		}
		if options.bootstrapService != nil {
			return errors.New("IAM bootstrap service is already configured")
		}
		options.bootstrapService = service
		return nil
	}
}

type requestContextKey int

const (
	subjectContextKey requestContextKey = iota
	sessionContextKey
	requestIDContextKey
)

func (api *readAPI) routes(group *echo.Group) {
	if api.bootstrapService != nil {
		group.POST("/bootstrap/complete", api.completeBootstrap, middleware.BodyLimit(64<<10))
	}
	protected := group.Group("", api.authenticate, middleware.BodyLimit(api.handler.maxBody))
	protected.GET("/bootstrap", api.bootstrap)
	protected.DELETE("/sessions/current", api.revokeCurrentSession)
	if api.oauthRepositories != nil {
		protected.GET("/oauth-clients", api.listOAuthClients)
		protected.POST("/oauth-clients", api.createOAuthClient)
		protected.PUT("/oauth-clients/:clientID", api.updateOAuthClient)
		protected.POST("/oauth-clients/:clientID/secret", api.rotateOAuthClientSecret)
		protected.POST("/oauth-clients/:clientID/enabled", api.setOAuthClientEnabled)
		protected.DELETE("/oauth-clients/:clientID", api.deleteOAuthClient)
		protected.DELETE("/oauth-clients/:clientID/consents/:identityID", api.revokeOAuthConsent)
	}
	if api.localUsers != nil {
		api.localUserRoutes(protected)
	}
}

func (api *readAPI) revokeCurrentSession(ctx *echo.Context) error {
	request := ctx.Request()
	stored, ok := request.Context().Value(sessionContextKey).(storage.AdminSession)
	if !ok || api.handler.sessions.Revoke(request.Context(), stored, requestID(request)) != nil {
		return writeError(ctx, http.StatusServiceUnavailable, "unavailable", "management session could not be revoked", requestID(request))
	}
	ctx.SetCookie(&http.Cookie{
		Name: api.handler.sessionCookieName, Value: "", Path: "/", Secure: api.handler.secureCookies, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
	ctx.SetCookie(&http.Cookie{
		Name: api.handler.csrfCookieName, Value: "", Path: "/", Secure: api.handler.secureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(echoContext *echo.Context) error {
		request := echoContext.Request()
		requestID := ensureRequestID(echoContext)
		if request.Header.Get("Authorization") != "" || request.Header.Get("Sec-Fetch-Site") == "cross-site" ||
			(request.Header.Get("Origin") != "" && request.Header.Get("Origin") != api.handler.origin) {
			return writeError(echoContext, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		}
		var token string
		cookieCount := 0
		for _, cookie := range request.Cookies() {
			if cookie.Name == api.handler.sessionCookieName {
				cookieCount++
				token = cookie.Value
			}
		}
		if cookieCount != 1 {
			return writeError(echoContext, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		}
		stored, subject, err := api.handler.sessions.AuthenticateSubject(request.Context(), token)
		if err != nil {
			return writeError(echoContext, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
			if err := adminsession.VerifyCSRF(stored, request.Header.Get(CSRFHeaderName)); err != nil {
				return writeError(echoContext, http.StatusForbidden, "csrf_failed", "management request was rejected", requestID)
			}
		}
		requestContext := context.WithValue(request.Context(), subjectContextKey, subject)
		requestContext = context.WithValue(requestContext, sessionContextKey, stored)
		requestContext = context.WithValue(requestContext, requestIDContextKey, requestID)
		echoContext.SetRequest(request.WithContext(requestContext))
		return next(echoContext)
	}
}

func requestID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDContextKey).(string)
	return value
}

func subjectFromRequest(request *http.Request) adminauthentication.Subject {
	value, _ := request.Context().Value(subjectContextKey).(adminauthentication.Subject)
	return value
}

func (api *readAPI) audit(
	request *http.Request,
	subject adminauthentication.Subject,
	action, outcome string,
) {
	metadata, err := json.Marshal(map[string]any{"authenticationType": subject.Authentication})
	if err != nil {
		return
	}
	_ = api.repositories.Audit().Append(request.Context(), storage.AuditEvent{
		ID: uuid.NewString(), IdentityID: subject.ID, Action: action,
		ResourceType: "management-api", Outcome: outcome, RequestID: requestID(request),
		Metadata: metadata, CreatedAt: time.Now().UTC(),
	})
}
