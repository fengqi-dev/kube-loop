package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	adminrevision "github.com/fengqi-dev/kube-loop/internal/controller/admin/revision"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controller/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controller/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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
	{Resource: adminauthorization.ResourceAssignment, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationRead},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationCreate},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationDryRun},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationPublish},
	{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationRollback},
	{Resource: adminauthorization.ResourcePrincipal, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceSession, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceTask, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceRelay, Operation: adminauthorization.OperationList},
	{Resource: adminauthorization.ResourceAudit, Operation: adminauthorization.OperationList},
}

func (api *readAPI) routes(router chi.Router) {
	router.Group(func(protected chi.Router) {
		protected.Use(api.authenticate)
		protected.Get("/capabilities", api.capabilities)
		protected.Delete("/sessions/current", api.revokeCurrentSession)
		protected.With(api.require(adminauthorization.Request{
			Resource: adminauthorization.ResourceStatus, Operation: adminauthorization.OperationRead,
		})).Get("/status", api.systemStatus)
		protected.With(api.require(adminauthorization.Request{
			Resource: adminauthorization.ResourcePrincipal, Operation: adminauthorization.OperationList,
		})).Get("/principals", api.listPrincipals)
		protected.Get("/sessions", api.listSessions)
		protected.Get("/tasks", api.listTasks)
		protected.With(api.require(adminauthorization.Request{
			Resource: adminauthorization.ResourceAudit, Operation: adminauthorization.OperationList,
		})).Get("/audit", api.listAudit)
		protected.With(api.require(adminauthorization.Request{
			Resource: adminauthorization.ResourceRelay, Operation: adminauthorization.OperationList,
		})).Get("/relays", api.listRelays)
		if api.policy != nil {
			protected.With(api.require(adminauthorization.Request{
				Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationRead,
			})).Get("/policy", api.currentPolicy)
			protected.With(api.require(adminauthorization.Request{
				Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationDryRun,
			})).Post("/policy/dry-run", api.dryRunPolicy)
			protected.With(api.require(adminauthorization.Request{
				Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationCreate,
			})).Post("/policy/drafts", api.createPolicyDraft)
			protected.With(api.require(adminauthorization.Request{
				Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationPublish,
			})).Post("/policy/changes/{changeID}/publish", api.publishPolicy)
			protected.With(api.require(adminauthorization.Request{
				Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationRollback,
			})).Post("/policy/rollback", api.rollbackPolicy)
		}
	})
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

func (api *readAPI) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := uuid.NewString()
		writer.Header().Set(managementRequestHeader, requestID)
		if request.Header.Get("Authorization") != "" || request.Header.Get("Sec-Fetch-Site") == "cross-site" ||
			(request.Header.Get("Origin") != "" && request.Header.Get("Origin") != api.handler.origin) {
			writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
			return
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
			return
		}
		stored, subject, err := api.handler.sessions.AuthenticateSubject(request.Context(), token)
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
			if err := adminsession.VerifyCSRF(stored, request.Header.Get(CSRFHeaderName)); err != nil {
				writeError(writer, http.StatusForbidden, "csrf_failed", "management request was rejected", requestID)
				return
			}
		}
		ctx := context.WithValue(request.Context(), subjectContextKey, subject)
		ctx = context.WithValue(ctx, sessionContextKey, stored)
		ctx = context.WithValue(ctx, requestIDContextKey, requestID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (api *readAPI) require(permission adminauthorization.Request) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			subject, ok := request.Context().Value(subjectContextKey).(adminauthorization.Subject)
			requestID, _ := request.Context().Value(requestIDContextKey).(string)
			if !ok || !api.authorizer.Authorize(request.Context(), subject, permission).Allowed {
				api.audit(request, subject, permission.Key(), "forbidden")
				writeError(writer, http.StatusForbidden, "forbidden", "management operation is not permitted", requestID)
				return
			}
			next.ServeHTTP(writer, request)
		})
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
		"controller": map[string]string{
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
