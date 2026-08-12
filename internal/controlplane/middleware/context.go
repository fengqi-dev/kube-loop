package middleware

import (
	"context"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

type principalContextKey struct{}

func PrincipalFromContext(ctx context.Context) (controlplaneapi.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(controlplaneapi.Principal)
	return principal, ok && principal.Subject != ""
}

type authorizationContextKey struct{}

type authorizationContextValue struct {
	request  authorization.Request
	decision authorization.Decision
}

func AuthorizationFromContext(ctx context.Context) (authorization.Request, authorization.Decision, bool) {
	value, ok := ctx.Value(authorizationContextKey{}).(authorizationContextValue)
	return value.request, value.decision, ok
}

type requestIDContextKey struct{}

func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

type auditContextKey struct{}

type auditContextState struct {
	sessionID string
}

func SetAuditSessionID(ctx context.Context, sessionID string) {
	if state, ok := ctx.Value(auditContextKey{}).(*auditContextState); ok && state != nil {
		state.sessionID = strings.TrimSpace(sessionID)
	}
}
