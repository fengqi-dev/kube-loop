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
	"net/netip"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

const (
	tokenEntropyBytes        = 32
	maximumBreakGlassTTL     = 15 * time.Minute
	normalSessionIdleTTL     = 15 * time.Minute
	normalSessionAbsoluteTTL = 8 * time.Hour
	breakGlassExchangeAudit  = "admin.session.break-glass.exchange"
	principalExchangeAudit   = "admin.session.principal.exchange"
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

type BreakGlassVerifier interface {
	Verify(context.Context, netip.Addr, []byte) (string, error)
	SessionTTL() time.Duration
	CurrentBreakGlassState(context.Context) (adminauthorization.BreakGlassState, error)
}

type Credentials struct {
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
}

type Service struct {
	store      Store
	breakGlass BreakGlassVerifier
	random     io.Reader
	now        func() time.Time
	newID      func() string
}

func New(store Store, breakGlassValues ...BreakGlassVerifier) (*Service, error) {
	if store == nil || len(breakGlassValues) > 1 {
		return nil, errors.New("management session storage is required")
	}
	var breakGlass BreakGlassVerifier
	if len(breakGlassValues) == 1 {
		breakGlass = breakGlassValues[0]
	}
	return &Service{store: store, breakGlass: breakGlass, random: rand.Reader, now: time.Now, newID: uuid.NewString}, nil
}

// ExchangeBreakGlass consumes the supplied emergency credential. The returned
// bearer and CSRF values are the only plaintext copies; storage receives hashes.
func (service *Service) ExchangeBreakGlass(
	ctx context.Context,
	source netip.Addr,
	credential []byte,
	requestID string,
) (Credentials, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		clear(credential)
		return Credentials{}, ErrAuthenticationFailed
	}
	succeeded := false
	defer func() {
		if !succeeded {
			service.recordFailedExchange(ctx, requestID)
		}
	}()
	generation, err := service.breakGlass.Verify(ctx, source, credential)
	if err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	ttl := service.breakGlass.SessionTTL()
	if ttl <= 0 || ttl > maximumBreakGlassTTL {
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
	expiresAt := now.Add(ttl)
	sessionHash := sha256.Sum256(sessionToken)
	csrfHash := sha256.Sum256(csrfToken)
	metadata, err := json.Marshal(map[string]any{
		"authenticationType": string(adminauthorization.AuthenticationBreakGlass),
		"expiresAt":          expiresAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	eventID := service.newID()
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if err := repositories.AdminSessions().Create(ctx, storage.AdminSession{
			IDHash:               sessionHash[:],
			AuthenticationType:   string(adminauthorization.AuthenticationBreakGlass),
			BreakGlassGeneration: generation,
			CSRFTokenHash:        csrfHash[:],
			CreatedAt:            now,
			LastSeenAt:           now,
			IdleExpiresAt:        expiresAt,
			AbsoluteExpiresAt:    expiresAt,
		}); err != nil {
			return err
		}
		return repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: eventID, Action: breakGlassExchangeAudit, ResourceType: "admin-session",
			Outcome: "success", RequestID: requestID, Metadata: metadata, CreatedAt: now,
		})
	})
	if err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	succeeded = true
	return Credentials{
		SessionToken: string(sessionToken),
		CSRFToken:    string(csrfToken),
		ExpiresAt:    expiresAt,
	}, nil
}

// ExchangePrincipal creates a browser-only Management Session from an already
// verified Gateway access-token identity. The Token Family is re-read inside
// the transaction so revocation racing the exchange fails closed.
func (service *Service) ExchangePrincipal(
	ctx context.Context,
	principalID, familyID string,
	authentication adminauthorization.AuthenticationType,
	requestID string,
) (Credentials, error) {
	principalID = strings.TrimSpace(principalID)
	familyID = strings.TrimSpace(familyID)
	requestID = strings.TrimSpace(requestID)
	if _, err := uuid.Parse(principalID); err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	if _, err := uuid.Parse(familyID); err != nil || requestID == "" ||
		(authentication != adminauthorization.AuthenticationNormal && authentication != adminauthorization.AuthenticationBootstrap) {
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
		family, err := repositories.TokenFamilies().GetByID(ctx, familyID)
		if err != nil || family.PrincipalID != principalID || family.RevokedAt != nil || !family.ExpiresAt.After(now) {
			return ErrAuthenticationFailed
		}
		if _, err := repositories.Principals().GetByID(ctx, principalID); err != nil {
			return ErrAuthenticationFailed
		}
		expiresAt = minTime(family.ExpiresAt, now.Add(normalSessionAbsoluteTTL))
		idleExpiresAt := minTime(expiresAt, now.Add(normalSessionIdleTTL))
		if !idleExpiresAt.After(now) {
			return ErrAuthenticationFailed
		}
		if err := repositories.AdminSessions().Create(ctx, storage.AdminSession{
			IDHash: sessionHash[:], PrincipalID: principalID, TokenFamilyID: familyID,
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
			ID: eventID, PrincipalID: principalID, Action: principalExchangeAudit,
			ResourceType: "admin-session", Outcome: "success", RequestID: requestID,
			Metadata: metadata, CreatedAt: now,
		})
	})
	if err != nil {
		return Credentials{}, ErrAuthenticationFailed
	}
	return Credentials{SessionToken: string(sessionToken), CSRFToken: string(csrfToken), ExpiresAt: expiresAt}, nil
}

func (service *Service) recordFailedExchange(ctx context.Context, requestID string) {
	metadata, err := json.Marshal(map[string]string{
		"authenticationType": string(adminauthorization.AuthenticationBreakGlass),
	})
	if err != nil {
		return
	}
	_ = service.store.Audit().Append(ctx, storage.AuditEvent{
		ID: service.newID(), Action: breakGlassExchangeAudit, ResourceType: "admin-session",
		Outcome: "failure", RequestID: requestID, Metadata: metadata, CreatedAt: service.now().UTC(),
	})
}

// Revoke atomically invalidates the current Management Session and records the
// logout without persisting its Cookie or CSRF plaintext values.
func (service *Service) Revoke(ctx context.Context, stored storage.AdminSession, requestID string) error {
	requestID = strings.TrimSpace(requestID)
	if len(stored.IDHash) != sha256.Size || requestID == "" {
		return ErrSessionInvalid
	}
	now := service.now().UTC()
	principalID := stored.PrincipalID
	if stored.AuthenticationType == string(adminauthorization.AuthenticationBreakGlass) {
		principalID = ""
	}
	metadata, err := json.Marshal(map[string]string{"authenticationType": stored.AuthenticationType})
	if err != nil {
		return ErrSessionInvalid
	}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if err := repositories.AdminSessions().Revoke(ctx, stored.IDHash, now); err != nil {
			return err
		}
		return repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: service.newID(), PrincipalID: principalID, Action: sessionRevokeAudit,
			ResourceType: "admin-session", Outcome: "success", RequestID: requestID,
			Metadata: metadata, CreatedAt: now,
		})
	})
	if err != nil {
		return ErrSessionInvalid
	}
	return nil
}

// Authenticate validates an opaque Management Session token and rejects a
// break-glass session immediately after its mounted Secret is rotated.
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
	if stored.AuthenticationType == string(adminauthorization.AuthenticationBreakGlass) {
		if service.breakGlass == nil {
			return storage.AdminSession{}, ErrSessionInvalid
		}
		state, err := service.breakGlass.CurrentBreakGlassState(ctx)
		if err != nil || !state.Enabled || !sameString(stored.BreakGlassGeneration, state.Generation) {
			return storage.AdminSession{}, ErrSessionInvalid
		}
	} else if stored.AuthenticationType == string(adminauthorization.AuthenticationNormal) ||
		stored.AuthenticationType == string(adminauthorization.AuthenticationBootstrap) {
		family, err := service.store.TokenFamilies().GetByID(ctx, stored.TokenFamilyID)
		if err != nil || family.PrincipalID != stored.PrincipalID || family.RevokedAt != nil || !family.ExpiresAt.After(now) {
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

// AuthenticateSubject resolves current Principal groups on every request, so
// group removal takes effect without waiting for the browser session to expire.
// Break-glass identity never consults or manufactures a Principal row.
func (service *Service) AuthenticateSubject(
	ctx context.Context,
	token string,
) (storage.AdminSession, adminauthorization.Subject, error) {
	stored, err := service.Authenticate(ctx, token)
	if err != nil {
		return storage.AdminSession{}, adminauthorization.Subject{}, err
	}
	subject := adminauthorization.Subject{Authentication: adminauthorization.AuthenticationType(stored.AuthenticationType)}
	if subject.Authentication == adminauthorization.AuthenticationBreakGlass {
		subject.ID = storage.ManagementActorBreakGlass
		subject.BreakGlassGeneration = stored.BreakGlassGeneration
		return stored, subject, nil
	}
	if subject.Authentication != adminauthorization.AuthenticationNormal &&
		subject.Authentication != adminauthorization.AuthenticationBootstrap {
		return storage.AdminSession{}, adminauthorization.Subject{}, ErrSessionInvalid
	}
	principal, err := service.store.Principals().GetByID(ctx, stored.PrincipalID)
	if err != nil {
		return storage.AdminSession{}, adminauthorization.Subject{}, ErrSessionInvalid
	}
	subject.ID = principal.ID
	subject.Groups = append([]string(nil), principal.Groups...)
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

func sameString(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
