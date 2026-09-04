package localuser

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const (
	ProviderID         = "local"
	minimumPasswordLen = 12
	maximumPasswordLen = 1024
)

var (
	ErrAuthenticationFailed = errors.New("local account authentication failed")
	ErrInvalidInput         = errors.New("local account input is invalid")
	ErrDisabled             = errors.New("local account is disabled")
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type User struct {
	IdentityID  string    `json:"identityId"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Email       string    `json:"email,omitempty"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type CreateRequest struct {
	Username    string
	Password    []byte
	DisplayName string
	Email       string
}

type Service struct {
	store     Store
	random    io.Reader
	now       func() time.Time
	newID     func() string
	dummyHash string
}

func New(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("local account storage is required")
	}
	dummy, err := hashPassword([]byte("not-a-real-password"), rand.Reader)
	if err != nil {
		return nil, errors.New("initialize password verifier")
	}
	return &Service{
		store:     store,
		random:    rand.Reader,
		now:       time.Now,
		newID:     uuid.NewString,
		dummyHash: dummy,
	}, nil
}

func (service *Service) Create(
	ctx context.Context,
	request CreateRequest,
) (User, error) {
	var result User
	err := service.store.WithinTransaction(
		ctx,
		func(repositories storage.Repositories) error {
			var createErr error
			result, createErr = service.CreateWithRepositories(
				ctx,
				repositories,
				request,
			)
			return createErr
		},
	)
	return result, err
}

func (service *Service) CreateWithRepositories(
	ctx context.Context,
	repositories storage.Repositories,
	request CreateRequest,
) (User, error) {
	if repositories == nil {
		return User{}, ErrInvalidInput
	}
	request.Username = utils.NormalizeUsername(request.Username)
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	request.Email = strings.TrimSpace(request.Email)
	if !validUsername(request.Username) ||
		len(request.Password) < minimumPasswordLen ||
		len(request.Password) > maximumPasswordLen {
		return User{}, ErrInvalidInput
	}
	passwordHash, err := hashPassword(request.Password, service.random)
	if err != nil {
		return User{}, err
	}
	now, identityID := service.now().UTC(), service.newID()
	identity := storage.Identity{
		ID:           identityID,
		Type:         "human",
		DisplayName:  request.DisplayName,
		PrimaryEmail: request.Email,
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if identity.DisplayName == "" {
		identity.DisplayName = request.Username
	}
	if _, err := repositories.Identities().Create(ctx, identity); err != nil {
		return User{}, err
	}
	credential := storage.PasswordCredential{
		IdentityID:   identity.ID,
		Username:     request.Username,
		PasswordHash: passwordHash,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := repositories.Credentials().CreatePassword(ctx, credential); err != nil {
		return User{}, err
	}
	return User{
		IdentityID:  identity.ID,
		Username:    request.Username,
		DisplayName: identity.DisplayName,
		Email:       identity.PrimaryEmail,
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}
