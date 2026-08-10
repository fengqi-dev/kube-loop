package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	admintoken "github.com/fengqi-dev/kube-loop/internal/controller/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/google/uuid"
)

type TokenAuthenticator interface {
	Authenticate(context.Context, string) (admintoken.AccessIdentity, error)
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

func (handler *Handler) exchangeToken(writer http.ResponseWriter, request *http.Request) {
	requestID := uuid.NewString()
	writer.Header().Set(managementRequestHeader, requestID)
	_, sourceKey := sourceAddress(request.RemoteAddr)
	if !handler.tokenLimit.allow(sourceKey) {
		writeError(writer, http.StatusTooManyRequests, "rate_limited", "management authentication failed", requestID)
		return
	}
	succeeded := false
	defer func() {
		if !succeeded {
			handler.recordTokenExchangeFailure(request, requestID)
		}
	}()
	if request.Header.Get("Origin") != handler.origin || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "invalid_request", "application/json is required", requestID)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, handler.maxBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input map[string]json.RawMessage
	if err := decoder.Decode(&input); err != nil || input == nil || len(input) != 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request", requestID)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request", requestID)
		return
	}
	accessToken, ok := bearerToken(request.Header.Values("Authorization"))
	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		return
	}
	identity, err := handler.tokenAuth.Authenticate(request.Context(), accessToken)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		return
	}
	authentication := adminauthorization.AuthenticationNormal
	bootstrapDecision := handler.readAPI.authorizer.Authorize(request.Context(), adminauthorization.Subject{
		ID: identity.Principal.ID, Groups: identity.Principal.Groups,
	}, adminauthorization.Request{Resource: adminauthorization.ResourceStatus, Operation: adminauthorization.OperationRead})
	if bootstrapDecision.Allowed && bootstrapDecision.Authentication == adminauthorization.AuthenticationBootstrap {
		authentication = adminauthorization.AuthenticationBootstrap
	}
	issued, err := handler.sessions.ExchangePrincipal(
		request.Context(), identity.Principal.ID, identity.FamilyID, authentication, requestID,
	)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name: SessionCookieName, Value: issued.SessionToken, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: max(1, int(time.Until(issued.ExpiresAt).Seconds())),
	})
	succeeded = true
	writeJSON(writer, http.StatusCreated, map[string]any{
		"csrfToken": issued.CSRFToken, "expiresAt": issued.ExpiresAt.Format(time.RFC3339Nano), "requestId": requestID,
	})
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 16<<10 {
		return "", false
	}
	return parts[1], true
}

func (handler *Handler) recordTokenExchangeFailure(request *http.Request, requestID string) {
	metadata, err := json.Marshal(map[string]string{"authenticationType": "bearer"})
	if err != nil {
		return
	}
	_ = handler.readAPI.status.Audit().Append(request.Context(), storage.AuditEvent{
		ID: uuid.NewString(), Action: "admin.session.principal.exchange", ResourceType: "admin-session",
		Outcome: "failure", RequestID: requestID, Metadata: metadata, CreatedAt: time.Now().UTC(),
	})
}
