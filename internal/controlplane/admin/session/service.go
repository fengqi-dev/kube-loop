package session

import (
	"crypto/rand"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
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
	return &Service{
		store:  store,
		random: rand.Reader,
		now:    time.Now,
		newID:  uuid.NewString,
	}, nil
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
