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

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/google/uuid"
)

const (
	tokenEntropyBytes       = 32
	maximumBreakGlassTTL    = 15 * time.Minute
	breakGlassExchangeAudit = "admin.session.break-glass.exchange"
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

func New(store Store, breakGlass BreakGlassVerifier) (*Service, error) {
	if store == nil || breakGlass == nil {
		return nil, errors.New("management session storage and break-glass verifier are required")
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
