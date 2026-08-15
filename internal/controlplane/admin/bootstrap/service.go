package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

const (
	TokenLifetime                 = 24 * time.Hour
	DefaultOrganizationName       = "KubeLoop"
	DefaultOrganizationSlug       = "kubeloop"
	DefaultAdministratorsGroup    = "Administrators"
	defaultAdministratorsGroupDoc = "Default organization administrators"
)

var (
	ErrAlreadyCompleted = errors.New("IAM bootstrap has already completed")
	ErrInvalidToken     = errors.New("IAM bootstrap token is invalid or expired")
	ErrInvalidRequest   = errors.New("IAM bootstrap request is invalid")
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type CompleteRequest struct {
	Token       string
	Username    string
	Password    []byte
	DisplayName string
	Email       string
	RequestID   string
}

type DefaultRequest struct {
	Username    string
	Password    []byte
	DisplayName string
	Email       string
	RequestID   string
}

type Result struct {
	Identity     adminlocaluser.User
	Organization storage.Organization
	Group        storage.Group
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
		return nil, errors.New("IAM bootstrap dependencies are required")
	}
	return &Service{store: store, localUsers: localUsers, random: rand.Reader, now: time.Now, newID: uuid.NewString}, nil
}

// EnsureToken creates the only bootstrap token. The plaintext is returned only
// on the call that persisted it and can never be reconstructed from storage.
func (service *Service) EnsureToken(ctx context.Context) (string, time.Time, error) {
	stored, err := service.store.BootstrapTokens().Get(ctx)
	if err == nil {
		return "", stored.ExpiresAt, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return "", time.Time{}, err
	}
	value := make([]byte, 32)
	if _, err := io.ReadFull(service.random, value); err != nil {
		return "", time.Time{}, errors.New("generate IAM bootstrap token")
	}
	plain := base64.RawURLEncoding.EncodeToString(value)
	clear(value)
	digest := sha256.Sum256([]byte(plain))
	now := service.now().UTC()
	token := storage.BootstrapToken{TokenHash: digest[:], CreatedAt: now, ExpiresAt: now.Add(TokenLifetime)}
	if err := service.store.BootstrapTokens().Create(ctx, token); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			stored, getErr := service.store.BootstrapTokens().Get(ctx)
			return "", stored.ExpiresAt, getErr
		}
		return "", time.Time{}, err
	}
	return plain, token.ExpiresAt, nil
}

func (service *Service) Complete(ctx context.Context, request CompleteRequest) (Result, error) {
	request.Token = strings.TrimSpace(request.Token)
	request.RequestID = strings.TrimSpace(request.RequestID)
	if request.Token == "" || uuid.Validate(request.RequestID) != nil {
		return Result{}, ErrInvalidRequest
	}
	digest := sha256.Sum256([]byte(request.Token))
	now := service.now().UTC()
	var result Result
	err := service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if err := repositories.BootstrapTokens().Consume(ctx, digest[:], now); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return ErrInvalidToken
			}
			return err
		}
		var createErr error
		result, createErr = service.createInitialGraph(ctx, repositories, initialGraphRequest{
			Username: request.Username, Password: request.Password, DisplayName: request.DisplayName, Email: request.Email,
			RequestID: request.RequestID, AuditAction: "iam.bootstrap.complete", Now: now,
		})
		return createErr
	})
	return result, err
}

// CompleteDefault initializes a completely empty IAM database. When no
// password is supplied, a generated password is returned only to the startup
// caller. A supplied password is never returned. The Kubernetes Secret remains
// the source of the initial credential, so it is immediately usable.
func (service *Service) CompleteDefault(ctx context.Context, request DefaultRequest) (Result, string, bool, error) {
	request.RequestID = strings.TrimSpace(request.RequestID)
	if uuid.Validate(request.RequestID) != nil {
		return Result{}, "", false, ErrInvalidRequest
	}
	passwordValue := request.Password
	generatedPassword := false
	if len(passwordValue) == 0 {
		passwordBytes := make([]byte, 24)
		if _, err := io.ReadFull(service.random, passwordBytes); err != nil {
			return Result{}, "", false, errors.New("generate initial administrator password")
		}
		passwordValue = []byte(base64.RawURLEncoding.EncodeToString(passwordBytes))
		clear(passwordBytes)
		generatedPassword = true
	}
	if generatedPassword {
		defer clear(passwordValue)
	}

	var result Result
	created := false
	err := service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		now := service.now().UTC()
		identities, err := repositories.Identities().List(ctx, storage.IdentityListFilter{Limit: 1})
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
		result, err = service.createInitialGraph(ctx, repositories, initialGraphRequest{
			Username: request.Username, Password: passwordValue, DisplayName: request.DisplayName, Email: request.Email,
			RequestID:   request.RequestID,
			AuditAction: "iam.bootstrap.default.complete", Now: now,
		})
		created = err == nil
		return err
	})
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
	identity, err := service.localUsers.CreateWithRepositories(ctx, repositories, adminlocaluser.CreateRequest{
		Username: request.Username, Password: request.Password, DisplayName: request.DisplayName, Email: request.Email,
	})
	if err != nil {
		return Result{}, err
	}
	organization := storage.Organization{ID: service.newID(), Name: DefaultOrganizationName,
		Slug: DefaultOrganizationSlug, Status: "active", CreatedAt: request.Now, UpdatedAt: request.Now}
	if err := repositories.Organizations().Create(ctx, organization); err != nil {
		return Result{}, err
	}
	if err := repositories.Organizations().AddMember(ctx, storage.OrganizationMembership{
		OrganizationID: organization.ID, IdentityID: identity.IdentityID, Status: "active",
		CreatedAt: request.Now, UpdatedAt: request.Now,
	}); err != nil {
		return Result{}, err
	}
	group := storage.Group{
		ID: service.newID(), OrganizationID: organization.ID, Name: DefaultAdministratorsGroup,
		Description: defaultAdministratorsGroupDoc, System: true, CreatedAt: request.Now, UpdatedAt: request.Now,
	}
	if err := repositories.Groups().Create(ctx, group); err != nil {
		return Result{}, err
	}
	if err := repositories.Groups().AddMember(ctx, storage.GroupMembership{
		GroupID: group.ID, IdentityID: identity.IdentityID, SourceType: "manual", CreatedAt: request.Now,
	}); err != nil {
		return Result{}, err
	}
	metadata, err := json.Marshal(map[string]string{"organizationId": organization.ID})
	if err != nil {
		return Result{}, errors.New("encode IAM bootstrap audit metadata")
	}
	if err := repositories.Audit().Append(ctx, storage.AuditEvent{ID: service.newID(), SchemaVersion: storage.ObjectSchemaVersion,
		IdentityID: identity.IdentityID, Action: request.AuditAction, ResourceType: "organization",
		ResourceID: organization.ID, Outcome: "success", RequestID: request.RequestID, Metadata: metadata,
		CreatedAt: request.Now}); err != nil {
		return Result{}, err
	}
	return Result{Identity: identity, Organization: organization, Group: group}, nil
}
