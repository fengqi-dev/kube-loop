package session

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

// ExchangeIdentity creates a browser-only Management Session from an already
// verified Gateway access-token identity. The OAuth grant is re-read inside
// the transaction so revocation racing the exchange fails closed.
func (service *Service) ExchangeIdentity(
	ctx context.Context,
	identityID, authorizationID string,
	authentication adminauthentication.Type,
	requestID string,
) (Credentials, error) {
	identityID = strings.TrimSpace(identityID)
	authorizationID = strings.TrimSpace(authorizationID)
	requestID = strings.TrimSpace(requestID)
	if _, err := uuid.Parse(identityID); err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	if _, err := uuid.Parse(authorizationID); err != nil || requestID == "" ||
		authentication != adminauthentication.Normal {
		return Credentials{}, ErrAuthenticationFailed
	}
	sessionToken, err := randomToken(service.random)
	if err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	defer clear(sessionToken)
	csrfToken, err := randomToken(service.random)
	if err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	defer clear(csrfToken)

	now := service.now().UTC()
	sessionHash := sha256.Sum256(sessionToken)
	csrfHash := sha256.Sum256(csrfToken)
	eventID := service.newID()
	var expiresAt time.Time
	err = service.store.WithinTransaction(
		ctx,
		func(repositories storage.Repositories) error {
			active, err := repositories.OAuthSessions().
				RequestActive(ctx, authorizationID, now)
			if err != nil || !active {
				return ErrAuthenticationFailed
			}
			if _, err := repositories.Identities().GetByID(ctx, identityID); err != nil {
				return ErrAuthenticationFailed
			}
			expiresAt = now.Add(normalSessionAbsoluteTTL)
			idleExpiresAt := minTime(expiresAt, now.Add(normalSessionIdleTTL))
			if !idleExpiresAt.After(now) {
				return ErrAuthenticationFailed
			}
			if err := repositories.AdminSessions().Create(ctx, storage.AdminSession{
				IDHash: sessionHash[:], IdentityID: identityID, AuthorizationID: authorizationID,
				AuthenticationType: string(authentication), CSRFTokenHash: csrfHash[:],
				CreatedAt: now, LastSeenAt: now, IdleExpiresAt: idleExpiresAt, AbsoluteExpiresAt: expiresAt,
			}); err != nil {
				return err
			}
			metadata, err := json.Marshal(map[string]any{
				"authenticationType": authentication,
				"expiresAt":          expiresAt.Format(time.RFC3339Nano),
			})
			if err != nil {
				return err
			}
			return repositories.Audit().Append(ctx, storage.AuditEvent{
				ID: eventID, IdentityID: identityID, Action: identityExchangeAudit,
				ResourceType: "admin-session", Outcome: "success", RequestID: requestID,
				Metadata: metadata, CreatedAt: now,
			})
		},
	)
	if err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	return Credentials{
		SessionToken: string(sessionToken),
		CSRFToken:    string(csrfToken),
		ExpiresAt:    expiresAt,
	}, nil
}

func randomToken(source io.Reader) ([]byte, error) {
	raw := make([]byte, tokenEntropyBytes)
	if _, err := io.ReadFull(source, raw); err != nil {
		clear(raw)
		return nil, err
	}
	encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(encoded, raw)
	clear(raw)
	return encoded, nil
}
