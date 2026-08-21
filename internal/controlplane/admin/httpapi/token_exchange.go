package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type TokenAuthenticator interface {
	Authenticate(context.Context, string) (authn.AccessIdentity, error)
}

func WithTokenExchange(authenticator TokenAuthenticator) Option {
	return func(options *handlerOptions) error {
		if authenticator == nil {
			return errors.New("management token authenticator is required")
		}
		if options.tokenAuthenticator != nil {
			return errors.New("management token exchange is already configured")
		}
		options.tokenAuthenticator = authenticator
		return nil
	}
}

func (handler *Handler) exchangeToken(ctx *echo.Context) error {
	request := ctx.Request()
	requestID := ensureRequestID(ctx)
	_, sourceKey := sourceAddress(request.RemoteAddr)
	if !handler.tokenLimit.allow(sourceKey) {
		return writeError(
			ctx,
			http.StatusTooManyRequests,
			"rate_limited",
			"management authentication failed",
			requestID,
		)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			handler.recordTokenExchangeFailure(request, requestID)
		}
	}()
	if request.Header.Get("Origin") != handler.origin ||
		request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return writeError(
			ctx,
			http.StatusUnauthorized,
			"unauthenticated",
			"management authentication failed",
			requestID,
		)
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return writeError(
			ctx,
			http.StatusUnsupportedMediaType,
			invalidRequestCode,
			"application/json is required",
			requestID,
		)
	}
	var input map[string]any
	if err := ctx.Bind(&input); err != nil || input == nil || len(input) != 0 {
		return writeError(
			ctx,
			http.StatusBadRequest, invalidRequestCode, "invalid request",
			requestID,
		)
	}
	accessToken, ok := bearerToken(request.Header.Values("Authorization"))
	if !ok {
		return writeError(
			ctx,
			http.StatusUnauthorized,
			"unauthenticated",
			"management authentication failed",
			requestID,
		)
	}
	identity, err := handler.tokenAuth.Authenticate(
		request.Context(),
		accessToken,
	)
	if err != nil {
		return writeError(
			ctx,
			http.StatusUnauthorized,
			"unauthenticated",
			"management authentication failed",
			requestID,
		)
	}
	issued, err := handler.sessions.ExchangeIdentity(
		request.Context(),
		identity.Identity.ID,
		identity.AuthorizationID,
		adminauthentication.Normal,
		requestID,
	)
	if err != nil {
		return writeError(
			ctx,
			http.StatusUnauthorized,
			"unauthenticated",
			"management authentication failed",
			requestID,
		)
	}
	handler.setSessionCookies(ctx, issued)
	succeeded = true
	return ctx.JSON(http.StatusCreated, map[string]any{
		"csrfToken": issued.CSRFToken, "expiresAt": issued.ExpiresAt.Format(time.RFC3339Nano), "requestId": requestID,
	})
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") ||
		len(parts[1]) > 16<<10 {
		return "", false
	}
	return parts[1], true
}

func (handler *Handler) recordTokenExchangeFailure(
	request *http.Request,
	requestID string,
) {
	metadata, err := json.Marshal(
		map[string]string{authenticationTypeField: "bearer"},
	)
	if err != nil {
		return
	}
	_ = handler.readAPI.repositories.Audit().
		Append(request.Context(), storage.AuditEvent{
			ID:     uuid.NewString(),
			Action: "admin.session.identity.exchange", ResourceType: "admin-session",
			Outcome: "failure", RequestID: requestID, Metadata: metadata, CreatedAt: time.Now().UTC(),
		})
}
