package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

var (
	ErrAlreadyCompleted = errors.New("iam bootstrap has already completed")
	ErrInvalidToken     = errors.New(
		"iam bootstrap token is invalid or expired",
	)
	ErrInvalidRequest = errors.New("iam bootstrap request is invalid")
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type DefaultRequest struct {
	Username    string
	Password    []byte
	DisplayName string
	Email       string
	RequestID   string
}

type Result struct {
	Identity adminlocaluser.User
}

type Service struct {
	store      Store
	localUsers *adminlocaluser.Service
	random     io.Reader
	now        func() time.Time
	newID      func() string
}

func New(store Store, localUsers *adminlocaluser.Service) (*Service, error) {
	if store == nil || localUsers == nil {
		return nil, errors.New("iam bootstrap dependencies are required")
	}
	return &Service{
		store:      store,
		localUsers: localUsers,
		random:     rand.Reader,
		now:        time.Now,
		newID:      uuid.NewString,
	}, nil
}

// CompleteDefault initializes a completely empty IAM database. When no
// password is supplied, a generated password is returned only to the startup
// caller. A supplied password is never returned. The Kubernetes Secret remains
// the source of the initial credential, so it is immediately usable.
func (service *Service) CompleteDefault(
	ctx context.Context,
	request DefaultRequest,
) (Result, string, bool, error) {
	request.RequestID = strings.TrimSpace(request.RequestID)
	if uuid.Validate(request.RequestID) != nil {
		return Result{}, "", false, ErrInvalidRequest
	}
	passwordValue := request.Password
	generatedPassword := false
	if len(passwordValue) == 0 {
		passwordBytes := make([]byte, 24)
		if _, err := io.ReadFull(service.random, passwordBytes); err != nil {
			return Result{}, "", false, errors.New(
				"generate initial administrator password",
			)
		}
		passwordValue = []byte(
			base64.RawURLEncoding.EncodeToString(passwordBytes),
		)
		clear(passwordBytes)
		generatedPassword = true
	}
	if generatedPassword {
		defer clear(passwordValue)
	}

	var result Result
	created := false
	err := service.store.WithinTransaction(
		ctx,
		func(repositories storage.Repositories) error {
			now := service.now().UTC()
			identities, err := repositories.Identities().
				List(ctx, storage.IdentityListFilter{Limit: 1})
			if err != nil {
				return err
			}
			if len(identities) != 0 {
				return nil
			}
			storedToken, err := repositories.BootstrapTokens().Get(ctx)
			if err == nil {
				if storedToken.ConsumedAt != nil {
					return ErrAlreadyCompleted
				}
				if err := repositories.BootstrapTokens().Consume(ctx, storedToken.TokenHash, now); err != nil {
					return err
				}
			} else if !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			result, err = service.createInitialGraph(
				ctx,
				repositories,
				initialGraphRequest{
					Username:    request.Username,
					Password:    passwordValue,
					DisplayName: request.DisplayName,
					Email:       request.Email,
					RequestID:   request.RequestID,
					AuditAction: "iam.bootstrap.default.complete",
					Now:         now,
				},
			)
			created = err == nil
			return err
		},
	)
	if err != nil {
		return Result{}, "", false, err
	}
	if !created {
		return Result{}, "", false, nil
	}
	if !generatedPassword {
		return result, "", true, nil
	}
	return result, string(passwordValue), true, nil
}

type initialGraphRequest struct {
	Username    string
	Password    []byte
	DisplayName string
	Email       string
	RequestID   string
	AuditAction string
	Now         time.Time
}

func (service *Service) createInitialGraph(
	ctx context.Context,
	repositories storage.Repositories,
	request initialGraphRequest,
) (Result, error) {
	identity, err := service.localUsers.CreateWithRepositories(
		ctx,
		repositories,
		adminlocaluser.CreateRequest{
			Username:    request.Username,
			Password:    request.Password,
			DisplayName: request.DisplayName,
			Email:       request.Email,
		},
	)
	if err != nil {
		return Result{}, err
	}
	metadata, err := json.Marshal(
		map[string]string{"authenticationType": "local"},
	)
	if err != nil {
		return Result{}, errors.New("encode IAM bootstrap audit metadata")
	}
	if err := repositories.Audit().Append(ctx, storage.AuditEvent{ID: service.newID(),
		IdentityID: identity.IdentityID, Action: request.AuditAction, ResourceType: "identity",
		ResourceID: identity.IdentityID, Outcome: "success", RequestID: request.RequestID, Metadata: metadata,
		CreatedAt: request.Now}); err != nil {
		return Result{}, err
	}
	return Result{Identity: identity}, nil
}
