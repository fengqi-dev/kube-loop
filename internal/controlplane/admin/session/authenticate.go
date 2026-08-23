package session

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"time"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

// Authenticate validates an opaque Management Session token.
func (service *Service) Authenticate(
	ctx context.Context,
	token string,
) (storage.AdminSession, error) {
	if !validOpaqueToken(token) {
		return storage.AdminSession{}, ErrSessionInvalid
	}
	digest := sha256.Sum256([]byte(token))
	stored, err := service.store.AdminSessions().GetByHash(ctx, digest[:])
	if err != nil {
		return storage.AdminSession{}, ErrSessionInvalid
	}
	now := service.now().UTC()
	if stored.RevokedAt != nil || !stored.IdleExpiresAt.After(now) ||
		!stored.AbsoluteExpiresAt.After(now) {
		return storage.AdminSession{}, ErrSessionInvalid
	}
	if stored.AuthenticationType == string(adminauthentication.Normal) {
		active, err := service.store.OAuthSessions().
			RequestActive(ctx, stored.AuthorizationID, now)
		if err != nil || !active {
			return storage.AdminSession{}, ErrSessionInvalid
		}
		if now.Sub(stored.LastSeenAt) >= time.Minute {
			nextIdleExpiry := minTime(
				stored.AbsoluteExpiresAt,
				now.Add(normalSessionIdleTTL),
			)
			if err := service.store.AdminSessions().Touch(
				ctx, stored.IDHash, stored.LastSeenAt, now, now, nextIdleExpiry,
			); err != nil {
				fresh, lookupErr := service.store.AdminSessions().
					GetByHash(ctx, stored.IDHash)
				if lookupErr != nil || fresh.RevokedAt != nil ||
					!fresh.IdleExpiresAt.After(now) ||
					!fresh.AbsoluteExpiresAt.After(
						now,
					) ||
					!fresh.LastSeenAt.After(stored.LastSeenAt) {
					return storage.AdminSession{}, ErrSessionInvalid
				}
				stored = fresh
			} else {
				stored.LastSeenAt = now
				stored.IdleExpiresAt = nextIdleExpiry
			}
		}
	} else {
		return storage.AdminSession{}, ErrSessionInvalid
	}
	return stored, nil
}

// AuthenticateSubject resolves current Identity groups on every request, so
// group removal takes effect without waiting for the browser session to expire.
func (service *Service) AuthenticateSubject(
	ctx context.Context,
	token string,
) (storage.AdminSession, adminauthentication.Subject, error) {
	stored, err := service.Authenticate(ctx, token)
	if err != nil {
		return storage.AdminSession{}, adminauthentication.Subject{}, err
	}
	subject := adminauthentication.Subject{
		Authentication: adminauthentication.Type(stored.AuthenticationType),
	}
	if subject.Authentication != adminauthentication.Normal {
		return storage.AdminSession{}, adminauthentication.Subject{}, ErrSessionInvalid
	}
	identity, err := service.store.Identities().GetByID(ctx, stored.IdentityID)
	if err != nil || identity.Status != statusActive {
		return storage.AdminSession{}, adminauthentication.Subject{}, ErrSessionInvalid
	}
	subject.ID = identity.ID
	return stored, subject, nil
}

func VerifyCSRF(stored storage.AdminSession, token string) error {
	if !validOpaqueToken(token) {
		return ErrCSRFInvalid
	}
	digest := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(digest[:], stored.CSRFTokenHash) != 1 {
		return ErrCSRFInvalid
	}
	return nil
}

func validOpaqueToken(token string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(tokenEntropyBytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	defer clear(decoded)
	return err == nil && len(decoded) == tokenEntropyBytes
}
