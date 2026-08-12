package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/operations"
	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
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
	readAPI            *readAPI
	tokenAuthenticator TokenAuthenticator
	relayStatus        RelayStatusSource
	policyService      *adminrevision.Service
	policyReloader     PolicyReloader
	providerService    *adminrevision.ProviderService
	operationService   *adminoperations.Service
}

type RelayStatusSource interface {
	Snapshot() []relayregistry.RelayStatus
}

type PolicyReloader interface {
	Load(context.Context) error
}

func WithPolicyAPI(service *adminrevision.Service, reloader PolicyReloader) Option {
	return func(options *handlerOptions) error {
		if service == nil || reloader == nil {
			return errors.New("management policy API dependencies are required")
		}
		if options.policyService != nil || options.policyReloader != nil {
			return errors.New("management policy API is already configured")
		}
		options.policyService, options.policyReloader = service, reloader
		return nil
	}
}

func WithProviderAPI(service *adminrevision.ProviderService) Option {
	return func(options *handlerOptions) error {
		if service == nil {
			return errors.New("management Provider API service is required")
		}
		if options.providerService != nil {
			return errors.New("management Provider API is already configured")
		}
		options.providerService = service
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
	handler    *Handler
	authorizer *adminauthorization.Engine
	status     StatusSource
	relays     RelayStatusSource
	build      BuildInfo
	policy     *adminrevision.Service
	reloader   PolicyReloader
	providers  *adminrevision.ProviderService
	operations *adminoperations.Service
}

type requestContextKey int

const (
	subjectContextKey requestContextKey = iota
	sessionContextKey
	requestIDContextKey
)

var capabilityChecks = []adminauthorization.Request{
	{Resource: adminauthorization.ResourceStatus, Operation: adminauthorization.OperationRead},
	{Resource: adminauthorization.ResourceConfiguration, Operation: adminauthorization.OperationRead},
	{Resource: adminauthorization.ResourceProvider, Operation: adminauthorization.OperationRead},
	{Resource: adminauthorization.ResourceProvider, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceProvider, Operation: adminauthorization.OperationCreate},
	{Resource: adminauthorization.ResourceProvider, Operation: adminauthorization.OperationValidate},
	{Resource: adminauthorization.ResourceProvider, Operation: adminauthorization.OperationPublish},
	{Resource: adminauthorization.ResourceProvider, Operation: adminauthorization.OperationRollback},
	{Resource: adminauthorization.ResourceAssignment, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationRead},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationCreate},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationDryRun},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationPublish},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationRollback},
	{Resource: adminauthorization.ResourcePrincipal, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceSession, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceSession, Operation: adminauthorization.OperationRevoke},
	{Resource: adminauthorization.ResourceSession, Operation: adminauthorization.OperationStop},
	{Resource: adminauthorization.ResourceTask, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceTask, Operation: adminauthorization.OperationStop},
	{Resource: adminauthorization.ResourceTask, Operation: adminauthorization.OperationRecover},
	{Resource: adminauthorization.ResourceRelay, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceRelay, Operation: adminauthorization.OperationDrain},
	{Resource: adminauthorization.ResourceRelay, Operation: adminauthorization.OperationRecover},
	{Resource: adminauthorization.ResourceAudit, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceAudit, Operation: adminauthorization.OperationExport},
}

func (api *readAPI) routes(router *echo.Echo) {
	protected := router.Group("", api.authenticate)
	protected.GET("/capabilities", adaptHandler(api.capabilities))
	protected.DELETE("/sessions/current", adaptHandler(api.revokeCurrentSession))
	protected.GET("/status", adaptHandler(api.systemStatus), api.permission(adminauthorization.ResourceStatus, adminauthorization.OperationRead))
	protected.GET("/principals", adaptHandler(api.listPrincipals), api.permission(adminauthorization.ResourcePrincipal, adminauthorization.OperationList))
	protected.GET("/sessions", adaptHandler(api.listSessions))
	protected.GET("/tasks", adaptHandler(api.listTasks))
	protected.GET("/audit", adaptHandler(api.listAudit), api.permission(adminauthorization.ResourceAudit, adminauthorization.OperationList))
	protected.GET("/relays", adaptHandler(api.listRelays), api.permission(adminauthorization.ResourceRelay, adminauthorization.OperationList))
	if api.policy != nil {
		protected.GET("/policy", adaptHandler(api.currentPolicy), api.permission(adminauthorization.ResourcePolicy, adminauthorization.OperationRead))
		protected.POST("/policy/dry-run", adaptHandler(api.dryRunPolicy), api.permission(adminauthorization.ResourcePolicy, adminauthorization.OperationDryRun))
		protected.POST("/policy/drafts", adaptHandler(api.createPolicyDraft), api.permission(adminauthorization.ResourcePolicy, adminauthorization.OperationCreate))
		protected.POST("/policy/changes/:changeID/publish", adaptHandler(api.publishPolicy), api.permission(adminauthorization.ResourcePolicy, adminauthorization.OperationPublish))
		protected.POST("/policy/rollback", adaptHandler(api.rollbackPolicy), api.permission(adminauthorization.ResourcePolicy, adminauthorization.OperationRollback))
	}
	if api.providers != nil {
		protected.GET("/providers", adaptHandler(api.listProviders), api.permission(adminauthorization.ResourceProvider, adminauthorization.OperationList))
		protected.GET("/providers/:providerID", adaptHandler(api.currentProvider), api.permission(adminauthorization.ResourceProvider, adminauthorization.OperationRead))
		protected.POST("/providers/:providerID/validate", adaptHandler(api.validateProvider), api.permission(adminauthorization.ResourceProvider, adminauthorization.OperationValidate))
		protected.POST("/providers/:providerID/drafts", adaptHandler(api.createProviderDraft), api.permission(adminauthorization.ResourceProvider, adminauthorization.OperationCreate))
		protected.POST("/providers/:providerID/changes/:changeID/publish", adaptHandler(api.publishProvider), api.permission(adminauthorization.ResourceProvider, adminauthorization.OperationPublish))
		protected.POST("/providers/:providerID/rollback", adaptHandler(api.rollbackProvider), api.permission(adminauthorization.ResourceProvider, adminauthorization.OperationRollback))
	}
	if api.operations != nil {
		protected.POST("/principals/:principalID/revoke", adaptHandler(api.revokePrincipalSessions), api.permission(adminauthorization.ResourceSession, adminauthorization.OperationRevoke))
		protected.POST("/principals/:principalID/device-sessions/:deviceSessionID/revoke", adaptHandler(api.revokeDeviceSession), api.permission(adminauthorization.ResourceSession, adminauthorization.OperationRevoke))
		protected.POST("/sessions/:sessionID/stop", adaptHandler(api.stopSession), api.permission(adminauthorization.ResourceSession, adminauthorization.OperationStop))
		protected.POST("/tasks/:taskID/stop", adaptHandler(api.stopTask), api.permission(adminauthorization.ResourceTask, adminauthorization.OperationStop))
		if api.operations.RelayAvailable() {
			protected.POST("/relays/:relayID/drain", adaptHandler(api.drainRelay), api.permission(adminauthorization.ResourceRelay, adminauthorization.OperationDrain))
			protected.POST("/relays/:relayID/recover", adaptHandler(api.recoverRelay), api.permission(adminauthorization.ResourceRelay, adminauthorization.OperationRecover))
		}
		if api.operations.RecoveryAvailable() {
			protected.POST("/tasks/recovery", adaptHandler(api.triggerRecovery), api.permission(adminauthorization.ResourceTask, adminauthorization.OperationRecover))
		}
		protected.POST("/audit/exports", adaptHandler(api.createAuditExport), api.permission(adminauthorization.ResourceAudit, adminauthorization.OperationExport))
		protected.GET("/audit/exports/:jobID", adaptHandler(api.getAuditExport), api.permission(adminauthorization.ResourceAudit, adminauthorization.OperationExport))
	}
}

func (api *readAPI) permission(resource adminauthorization.Resource, operation adminauthorization.Operation) echo.MiddlewareFunc {
	return api.require(adminauthorization.Request{Resource: resource, Operation: operation})
}

func (api *readAPI) revokeCurrentSession(writer http.ResponseWriter, request *http.Request) {
	stored, ok := request.Context().Value(sessionContextKey).(storage.AdminSession)
	if !ok || api.handler.sessions.Revoke(request.Context(), stored, requestID(request)) != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management session could not be revoked", requestID(request))
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
	writer.WriteHeader(http.StatusNoContent)
}

func (api *readAPI) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(echoContext *echo.Context) error {
		writer, request := echoContext.Response(), echoContext.Request()
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

func (api *readAPI) capabilities(writer http.ResponseWriter, request *http.Request) {
	subject, _ := request.Context().Value(subjectContextKey).(adminauthorization.Subject)
	capabilities := make([]string, 0, len(capabilityChecks))
	for _, permission := range capabilityChecks {
		if api.authorizer.Authorize(request.Context(), subject, permission).Allowed {
			capabilities = append(capabilities, permission.Key())
		}
	}
	sort.Strings(capabilities)
	namespaceScopes := make([]map[string]any, 0)
	for _, namespace := range api.authorizer.DelegatedNamespaces(subject) {
		allowed := make([]string, 0, len(capabilityChecks))
		for _, permission := range capabilityChecks {
			permission.Namespace = namespace
			if api.authorizer.Authorize(request.Context(), subject, permission).Allowed {
				allowed = append(allowed, permission.Key())
			}
		}
		sort.Strings(allowed)
		if len(allowed) > 0 {
			namespaceScopes = append(namespaceScopes, map[string]any{"namespace": namespace, "capabilities": allowed})
		}
	}
	api.audit(request, subject, "admin.capabilities/read", "success")
	writeJSON(writer, http.StatusOK, map[string]any{
		"authenticationType": subject.Authentication,
		"capabilities":       capabilities,
		"namespaceScopes":    namespaceScopes,
		"policyRevision":     api.authorizer.Revision(),
		"policyEtag":         api.authorizer.ETag(),
	})
}

func (api *readAPI) systemStatus(writer http.ResponseWriter, request *http.Request) {
	if err := api.status.Check(request.Context()); err != nil {
		api.audit(request, subjectFromRequest(request), "admin.status/read", "failure")
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management status is unavailable", requestID(request))
		return
	}
	schemaVersion, err := api.status.SchemaVersion(request.Context())
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.status/read", "failure")
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management status is unavailable", requestID(request))
		return
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
		"managementPolicy": map[string]any{
			"status": "ready", "revision": api.authorizer.Revision(), "etag": api.authorizer.ETag(),
		},
	})
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
	principalID := subject.ID
	if subject.Authentication == adminauthorization.AuthenticationBreakGlass {
		principalID = ""
	}
	metadata, err := json.Marshal(map[string]any{
		"authenticationType": subject.Authentication,
		"policyRevision":     api.authorizer.Revision(),
	})
	if err != nil {
		return
	}
	_ = api.status.Audit().Append(request.Context(), storage.AuditEvent{
		ID: uuid.NewString(), PrincipalID: principalID, Action: action,
		ResourceType: "management-api", Outcome: outcome, RequestID: requestID(request),
		Metadata: metadata, CreatedAt: time.Now().UTC(),
	})
}
