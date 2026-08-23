package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	adminbootstrap "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/bootstrap"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type readAPI struct {
	handler           *Handler
	repositories      storage.Repositories
	localUsers        *adminlocaluser.Service
	bootstrapService  *adminbootstrap.Service
	oauthRepositories storage.Repositories
	oauthTransactions storage.TransactionManager
}

func (api *readAPI) routes(group *echo.Group) {
	if api.bootstrapService != nil {
		group.POST(
			"/bootstrap/complete",
			api.completeBootstrap,
			middleware.BodyLimit(64<<10),
		)
	}
	protected := group.Group(
		"",
		api.authenticate,
		middleware.BodyLimit(api.handler.maxBody),
	)
	protected.GET("/bootstrap", api.bootstrap)
	protected.DELETE("/sessions/current", api.revokeCurrentSession)
	if api.oauthRepositories != nil {
		protected.GET("/oauth-clients", api.listOAuthClients)
		protected.POST("/oauth-clients", api.createOAuthClient)
		protected.PUT("/oauth-clients/:clientID", api.updateOAuthClient)
		protected.POST(
			"/oauth-clients/:clientID/secret",
			api.rotateOAuthClientSecret,
		)
		protected.POST(
			"/oauth-clients/:clientID/enabled",
			api.setOAuthClientEnabled,
		)
		protected.DELETE("/oauth-clients/:clientID", api.deleteOAuthClient)
		protected.DELETE(
			"/oauth-clients/:clientID/consents/:identityID",
			api.revokeOAuthConsent,
		)
	}
	if api.localUsers != nil {
		api.localUserRoutes(protected)
	}
}

func (api *readAPI) revokeCurrentSession(ctx *echo.Context) error {
	request := ctx.Request()
	stored, ok := request.Context().Value(sessionContextKey).(storage.AdminSession)
	if !ok ||
		api.handler.sessions.Revoke(
			request.Context(),
			stored,
			requestID(request),
		) != nil {
		return writeError(
			ctx,
			http.StatusServiceUnavailable,
			"unavailable",
			"management session could not be revoked",
			requestID(request),
		)
	}
	//nolint:gosec // Secure follows the validated public URL; this only expires the existing cookie.
	ctx.SetCookie(&http.Cookie{
		Name: api.handler.sessionCookieName, Value: "", Path: "/", Secure: api.handler.secureCookies, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
	//nolint:gosec // The script-readable double-submit CSRF cookie is intentionally expired with matching attributes.
	ctx.SetCookie(&http.Cookie{
		Name: api.handler.csrfCookieName, Value: "", Path: "/", Secure: api.handler.secureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) audit(
	request *http.Request,
	subject adminauthentication.Subject,
	action, outcome string,
) {
	metadata, err := json.Marshal(
		map[string]any{authenticationTypeField: subject.Authentication},
	)
	if err != nil {
		return
	}
	_ = api.repositories.Audit().Append(request.Context(), storage.AuditEvent{
		ID: uuid.NewString(), IdentityID: subject.ID, Action: action,
		ResourceType: "management-api", Outcome: outcome, RequestID: requestID(request),
		Metadata: metadata, CreatedAt: time.Now().UTC(),
	})
}
