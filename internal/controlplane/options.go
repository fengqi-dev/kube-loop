package controlplane

import (
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/health"
)

type serverOptions struct {
	authenticator     controlplaneapi.Authenticator
	authorizer        authorization.Authorizer
	audit             AuditSink
	apiRoutes         RouteRegistrar
	readiness         health.Checker
	authRoutes        RouteRegistrar
	managementHandler http.Handler
	authMethodSource  AuthMethodSource
}

type AuthMethodSource interface {
	AuthMethods() []AuthMethod
}

type AuthMethodSourceFunc func() []AuthMethod

func (f AuthMethodSourceFunc) AuthMethods() []AuthMethod { return f() }

type ServerOption func(*serverOptions)

func WithAuthMethodSource(source AuthMethodSource) ServerOption {
	return func(options *serverOptions) { options.authMethodSource = source }
}

func WithAuthenticator(authenticator controlplaneapi.Authenticator) ServerOption {
	return func(options *serverOptions) { options.authenticator = authenticator }
}

func WithAuthorizer(authorizer authorization.Authorizer) ServerOption {
	return func(options *serverOptions) { options.authorizer = authorizer }
}

func WithAuditSink(sink AuditSink) ServerOption {
	return func(options *serverOptions) { options.audit = sink }
}

func WithAPIRoutes(routes RouteRegistrar) ServerOption {
	return func(options *serverOptions) { options.apiRoutes = routes }
}

func WithReadinessChecker(checker health.Checker) ServerOption {
	return func(options *serverOptions) { options.readiness = checker }
}

func WithAuthRoutes(routes RouteRegistrar) ServerOption {
	return func(options *serverOptions) { options.authRoutes = routes }
}

// WithManagementHandler installs the browser-only Management Plane below
// /kubeloop/api/admin without passing it through the ordinary Gateway Bearer chain.
func WithManagementHandler(handler http.Handler) ServerOption {
	return func(options *serverOptions) { options.managementHandler = handler }
}
