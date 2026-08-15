package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminbootstrap "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/bootstrap"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/operations"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

type StatusSource interface {
	storage.Repositories
	Check(context.Context) error
	Backend() storage.Backend
	SchemaVersion(context.Context) (int, error)
	Audit() storage.AuditRepository
}

type BuildInfo struct {
	Version     string
	Commit      string
	ProtocolMin string
	ProtocolMax string
}

type Option func(*handlerOptions) error

type handlerOptions struct {
	readAPI               *readAPI
	tokenAuthenticator    TokenAuthenticator
	relayStatus           RelayStatusSource
	authorizationReloader PolicyReloader
	operationService      *adminoperations.Service
	localUsers            *adminlocaluser.Service
	bootstrapService      *adminbootstrap.Service
	oauthRepositories     storage.Repositories
	oauthTransactions     storage.TransactionManager
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

type RelayStatusSource interface {
	Snapshot() []relayregistry.RelayStatus
}

type PolicyReloader interface {
	Load(context.Context) error
}

func WithIAM(reloader PolicyReloader) Option {
	return func(options *handlerOptions) error {
		if reloader == nil {
			return errors.New("IAM API authorization reloader is required")
		}
		if options.authorizationReloader != nil {
			return errors.New("IAM API is already configured")
		}
		options.authorizationReloader = reloader
		return nil
	}
}

func WithOperationsAPI(service *adminoperations.Service) Option {
	return func(options *handlerOptions) error {
		if service == nil {
			return errors.New("management Operations API service is required")
		}
		if options.operationService != nil {
			return errors.New("management Operations API is already configured")
		}
		options.operationService = service
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

func WithReadAPI(authorizer *adminauthorization.Engine, status StatusSource, build BuildInfo) Option {
	return func(options *handlerOptions) error {
		if authorizer == nil || status == nil {
			return errors.New("management read API authorizer and status source are required")
		}
		if options.readAPI != nil {
			return errors.New("management read API is already configured")
		}
		build.Version = strings.TrimSpace(build.Version)
		build.Commit = strings.TrimSpace(build.Commit)
		build.ProtocolMin = strings.TrimSpace(build.ProtocolMin)
		build.ProtocolMax = strings.TrimSpace(build.ProtocolMax)
		if build.Version == "" || build.Commit == "" || build.ProtocolMin == "" || build.ProtocolMax == "" {
			return errors.New("management read API build information is required")
		}
		options.readAPI = &readAPI{authorizer: authorizer, status: status, build: build}
		return nil
	}
}

func WithRelayStatusSource(source RelayStatusSource) Option {
	return func(options *handlerOptions) error {
		if source == nil {
			return errors.New("management Relay status source is required")
		}
		if options.relayStatus != nil {
			return errors.New("management Relay status source is already configured")
		}
		options.relayStatus = source
		return nil
	}
}

type readAPI struct {
	handler               *Handler
	authorizer            *adminauthorization.Engine
	status                StatusSource
	relays                RelayStatusSource
	build                 BuildInfo
	authorizationReloader PolicyReloader
	operations            *adminoperations.Service
	localUsers            *adminlocaluser.Service
	bootstrapService      *adminbootstrap.Service
	oauthRepositories     storage.Repositories
	oauthTransactions     storage.TransactionManager
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
		group.POST("/bootstrap/complete", api.completeBootstrap)
	}
	if api.localUsers != nil && api.oauthTransactions != nil {
		group.POST("/invitations/accept", api.acceptInvitation)
	}
	protected := group.Group("", api.authenticate)
	protected.GET("/bootstrap", api.bootstrap)
	protected.GET("/authorization/effective", api.effectiveAuthorization)
	protected.DELETE("/sessions/current", api.revokeCurrentSession)
	protected.GET("/overview", api.overview, api.permission(adminauthorization.ResourceStatus, adminauthorization.OperationRead))
	protected.GET("/status", api.systemStatus, api.permission(adminauthorization.ResourceStatus, adminauthorization.OperationRead))
	protected.GET("/identities", api.listIdentities, api.permission(adminauthorization.ResourceIdentity, adminauthorization.OperationList))
	protected.GET("/sessions", api.listSessions)
	protected.GET("/oauth-grants", api.listOAuthGrants,
		api.permission(adminauthorization.ResourceSession, adminauthorization.OperationList))
	protected.GET("/tasks", api.listTasks)
	protected.GET("/audit", api.listAudit, api.permission(adminauthorization.ResourceAudit, adminauthorization.OperationList))
	protected.GET("/relays", api.listRelays, api.permission(adminauthorization.ResourceRelay, adminauthorization.OperationList))
	if api.authorizationReloader != nil {
		api.iamRoutes(protected)
	}
	if api.oauthRepositories != nil {
		protected.GET("/oauth-clients", api.listOAuthClients)
		protected.POST("/oauth-clients", api.createOAuthClient)
		protected.PUT("/oauth-clients/:clientID", api.updateOAuthClient)
		protected.POST("/oauth-clients/:clientID/secret", api.rotateOAuthClientSecret)
		protected.POST("/oauth-clients/:clientID/enabled", api.setOAuthClientEnabled)
		protected.DELETE("/oauth-clients/:clientID", api.deleteOAuthClient)
		protected.DELETE("/oauth-clients/:clientID/consents/:identityID", api.revokeOAuthConsent)
	}
	if api.operations != nil {
		protected.POST("/identities/:identityID/revoke", api.revokeIdentitySessions, api.permission(adminauthorization.ResourceSession, adminauthorization.OperationRevoke))
		protected.POST("/identities/:identityID/oauth-grants/:authorizationID/revoke", api.revokeOAuthGrant, api.permission(adminauthorization.ResourceSession, adminauthorization.OperationRevoke))
		protected.POST("/sessions/:sessionID/stop", api.stopSession, api.permission(adminauthorization.ResourceSession, adminauthorization.OperationStop))
		protected.POST("/tasks/:taskID/stop", api.stopTask, api.permission(adminauthorization.ResourceTask, adminauthorization.OperationStop))
		if api.operations.RelayAvailable() {
			protected.POST("/relays/:relayID/drain", api.drainRelay, api.permission(adminauthorization.ResourceRelay, adminauthorization.OperationDrain))
			protected.POST("/relays/:relayID/recover", api.recoverRelay, api.permission(adminauthorization.ResourceRelay, adminauthorization.OperationRecover))
		}
		if api.operations.RecoveryAvailable() {
			protected.POST("/tasks/recovery", api.triggerRecovery, api.permission(adminauthorization.ResourceTask, adminauthorization.OperationRecover))
		}
		protected.POST("/audit/exports", api.createAuditExport, api.permission(adminauthorization.ResourceAudit, adminauthorization.OperationExport))
		protected.GET("/audit/exports/:jobID", api.getAuditExport, api.permission(adminauthorization.ResourceAudit, adminauthorization.OperationExport))
	}
	if api.localUsers != nil {
		api.localUserRoutes(protected)
	}
}

func (api *readAPI) permission(resource adminauthorization.Resource, operation adminauthorization.Operation) echo.MiddlewareFunc {
	return api.require(adminauthorization.Request{Resource: resource, Operation: operation})
}

func (api *readAPI) revokeCurrentSession(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	stored, ok := request.Context().Value(sessionContextKey).(storage.AdminSession)
	if !ok || api.handler.sessions.Revoke(request.Context(), stored, requestID(request)) != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management session could not be revoked", requestID(request))
		return nil
	}
	http.SetCookie(writer, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
	http.SetCookie(writer, &http.Cookie{
		Name: CSRFCookieName, Value: "", Path: "/", Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
	writer.WriteHeader(http.StatusNoContent)
	return nil
}

func (api *readAPI) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(echoContext *echo.Context) error {
		writer, request := echoContext.Response(), echoContext.Request()
		request.Body = http.MaxBytesReader(writer, request.Body, api.handler.maxBody)
		requestID := ensureRequestID(writer, request)
		if request.Header.Get("Authorization") != "" || request.Header.Get("Sec-Fetch-Site") == "cross-site" ||
			(request.Header.Get("Origin") != "" && request.Header.Get("Origin") != api.handler.origin) {
			writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
			return nil
		}
		var token string
		cookieCount := 0
		for _, cookie := range request.Cookies() {
			if cookie.Name == SessionCookieName {
				cookieCount++
				token = cookie.Value
			}
		}
		if cookieCount != 1 {
			writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
			return nil
		}
		stored, subject, err := api.handler.sessions.AuthenticateSubject(request.Context(), token)
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
			return nil
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
			if err := adminsession.VerifyCSRF(stored, request.Header.Get(CSRFHeaderName)); err != nil {
				writeError(writer, http.StatusForbidden, "csrf_failed", "management request was rejected", requestID)
				return nil
			}
		}
		requestContext := context.WithValue(request.Context(), subjectContextKey, subject)
		requestContext = context.WithValue(requestContext, sessionContextKey, stored)
		requestContext = context.WithValue(requestContext, requestIDContextKey, requestID)
		echoContext.SetRequest(request.WithContext(requestContext))
		return next(echoContext)
	}
}

func (api *readAPI) require(permission adminauthorization.Request) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(echoContext *echo.Context) error {
			writer, request := echoContext.Response(), echoContext.Request()
			subject, ok := request.Context().Value(subjectContextKey).(adminauthorization.Subject)
			requestID, _ := request.Context().Value(requestIDContextKey).(string)
			if !ok || !api.authorizer.Authorize(request.Context(), subject, permission).Allowed {
				api.audit(request, subject, permission.Key(), "forbidden")
				writeError(writer, http.StatusForbidden, "forbidden", "management operation is not permitted", requestID)
				return nil
			}
			return next(echoContext)
		}
	}
}

func (api *readAPI) systemStatus(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	if err := api.status.Check(request.Context()); err != nil {
		api.audit(request, subjectFromRequest(request), "admin.status/read", "failure")
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management status is unavailable", requestID(request))
		return nil
	}
	schemaVersion, err := api.status.SchemaVersion(request.Context())
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.status/read", "failure")
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management status is unavailable", requestID(request))
		return nil
	}
	api.audit(request, subjectFromRequest(request), "admin.status/read", "success")
	writeJSON(writer, http.StatusOK, map[string]any{
		"controlPlane": map[string]string{
			"version": api.build.Version, "commit": api.build.Commit,
			"protocolMin": api.build.ProtocolMin, "protocolMax": api.build.ProtocolMax,
		},
		"storage": map[string]any{
			"status": "ready", "backend": api.status.Backend(), "schemaVersion": schemaVersion,
		},
		"managementPolicy": map[string]any{"status": "ready"},
	})
	return nil
}

func requestID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDContextKey).(string)
	return value
}

func subjectFromRequest(request *http.Request) adminauthorization.Subject {
	value, _ := request.Context().Value(subjectContextKey).(adminauthorization.Subject)
	return value
}

func (api *readAPI) audit(
	request *http.Request,
	subject adminauthorization.Subject,
	action, outcome string,
) {
	identityID := subject.ID
	if subject.Authentication == adminauthorization.AuthenticationBreakGlass {
		identityID = ""
	}
	metadata, err := json.Marshal(map[string]any{"authenticationType": subject.Authentication})
	if err != nil {
		return
	}
	_ = api.status.Audit().Append(request.Context(), storage.AuditEvent{
		ID: uuid.NewString(), IdentityID: identityID, Action: action,
		ResourceType: "management-api", Outcome: outcome, RequestID: requestID(request),
		Metadata: metadata, CreatedAt: time.Now().UTC(),
	})
}
