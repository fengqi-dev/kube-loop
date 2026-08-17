// Package session issues and validates browser-only Management Plane sessions.
// It is deliberately separate from ordinary Gateway bearer-token sessions.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

const (
	tokenEntropyBytes        = 32
	normalSessionIdleTTL     = 15 * time.Minute
	normalSessionAbsoluteTTL = 8 * time.Hour
	identityExchangeAudit    = "admin.session.identity.exchange"
	sessionRevokeAudit       = "admin.session.revoke"
)

var (
	ErrAuthenticationFailed = errors.New("management authentication failed")
	ErrSessionInvalid       = errors.New("management session is invalid")
	ErrCSRFInvalid          = errors.New("management CSRF token is invalid")
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type Credentials struct {
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
}

type Service struct {
	store  Store
	random io.Reader
	now    func() time.Time
	newID  func() string
}

func New(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("management session storage is required")
	}
	return &Service{store: store, random: rand.Reader, now: time.Now, newID: uuid.NewString}, nil
}

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
	if _, err := uuid.Parse(authorizationID); err != nil || requestID == "" || authentication != adminauthentication.Normal {
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
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		active, err := repositories.OAuthSessions().RequestActive(ctx, authorizationID, now)
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
	})
	if err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	return Credentials{SessionToken: string(sessionToken), CSRFToken: string(csrfToken), ExpiresAt: expiresAt}, nil
}

// Revoke atomically invalidates the current Management Session and records the
// logout without persisting its Cookie or CSRF plaintext values.
func (service *Service) Revoke(ctx context.Context, stored storage.AdminSession, requestID string) error {
	requestID = strings.TrimSpace(requestID)
	if len(stored.IDHash) != sha256.Size || requestID == "" {
		return ErrSessionInvalid
	}
	now := service.now().UTC()
	identityID := stored.IdentityID
	metadata, err := json.Marshal(map[string]string{"authenticationType": stored.AuthenticationType})
	if err != nil {
		return ErrSessionInvalid
	}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if err := repositories.AdminSessions().Revoke(ctx, stored.IDHash, now); err != nil {
			return err
		}
		return repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: service.newID(), IdentityID: identityID, Action: sessionRevokeAudit,
			ResourceType: "admin-session", Outcome: "success", RequestID: requestID,
			Metadata: metadata, CreatedAt: now,
		})
	})
	if err != nil {
		return ErrSessionInvalid
	}
	return nil
}

// Authenticate validates an opaque Management Session token.
func (service *Service) Authenticate(ctx context.Context, token string) (storage.AdminSession, error) {
	if !validOpaqueToken(token) {
		return storage.AdminSession{}, ErrSessionInvalid
	}
	digest := sha256.Sum256([]byte(token))
	stored, err := service.store.AdminSessions().GetByHash(ctx, digest[:])
	if err != nil {
		return storage.AdminSession{}, ErrSessionInvalid
	}
	now := service.now().UTC()
	if stored.RevokedAt != nil || !stored.IdleExpiresAt.After(now) || !stored.AbsoluteExpiresAt.After(now) {
		return storage.AdminSession{}, ErrSessionInvalid
	}
	if stored.AuthenticationType == string(adminauthentication.Normal) {
		active, err := service.store.OAuthSessions().RequestActive(ctx, stored.AuthorizationID, now)
		if err != nil || !active {
			return storage.AdminSession{}, ErrSessionInvalid
		}
		if now.Sub(stored.LastSeenAt) >= time.Minute {
			nextIdleExpiry := minTime(stored.AbsoluteExpiresAt, now.Add(normalSessionIdleTTL))
			if err := service.store.AdminSessions().Touch(
				ctx, stored.IDHash, stored.LastSeenAt, now, now, nextIdleExpiry,
			); err != nil {
				fresh, lookupErr := service.store.AdminSessions().GetByHash(ctx, stored.IDHash)
				if lookupErr != nil || fresh.RevokedAt != nil || !fresh.IdleExpiresAt.After(now) ||
					!fresh.AbsoluteExpiresAt.After(now) || !fresh.LastSeenAt.After(stored.LastSeenAt) {
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
	subject := adminauthentication.Subject{Authentication: adminauthentication.Type(stored.AuthenticationType)}
	if subject.Authentication != adminauthentication.Normal {
		return storage.AdminSession{}, adminauthentication.Subject{}, ErrSessionInvalid
	}
	identity, err := service.store.Identities().GetByID(ctx, stored.IdentityID)
	if err != nil || identity.Status != "active" {
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

func validOpaqueToken(token string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(tokenEntropyBytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	defer clear(decoded)
	return err == nil && len(decoded) == tokenEntropyBytes
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
