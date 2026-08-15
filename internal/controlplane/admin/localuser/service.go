// Package localuser manages KubeLoop password identities.
package localuser

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
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
	return &Service{store: store, random: rand.Reader, now: time.Now, newID: uuid.NewString, dummyHash: dummy}, nil
}

func (service *Service) Create(ctx context.Context, request CreateRequest) (User, error) {
	var result User
	err := service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		var createErr error
		result, createErr = service.CreateWithRepositories(ctx, repositories, request)
		return createErr
	})
	return result, err
}

func (service *Service) CreateWithRepositories(ctx context.Context, repositories storage.Repositories, request CreateRequest) (User, error) {
	if repositories == nil {
		return User{}, ErrInvalidInput
	}
	request.Username = normalizeUsername(request.Username)
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	request.Email = strings.TrimSpace(request.Email)
	if !validUsername(request.Username) || len(request.Password) < minimumPasswordLen || len(request.Password) > maximumPasswordLen {
		return User{}, ErrInvalidInput
	}
	passwordHash, err := hashPassword(request.Password, service.random)
	if err != nil {
		return User{}, err
	}
	now, identityID := service.now().UTC(), service.newID()
	identity := storage.Identity{ID: identityID, Type: "human", DisplayName: request.DisplayName,
		PrimaryEmail: request.Email, Status: "active", CreatedAt: now, UpdatedAt: now}
	if identity.DisplayName == "" {
		identity.DisplayName = request.Username
	}
	if _, err := repositories.Identities().Create(ctx, identity); err != nil {
		return User{}, err
	}
	credential := storage.PasswordCredential{IdentityID: identity.ID, Username: request.Username,
		PasswordHash: passwordHash, Enabled: true,
		CreatedAt: now, UpdatedAt: now}
	if err := repositories.Credentials().CreatePassword(ctx, credential); err != nil {
		return User{}, err
	}
	return User{IdentityID: identity.ID, Username: request.Username, DisplayName: identity.DisplayName,
		Email: identity.PrimaryEmail, Enabled: true, CreatedAt: now, UpdatedAt: now}, nil
}

func (service *Service) Authenticate(ctx context.Context, username string, password []byte, requestIDs ...string) (User, error) {
	requestID := ""
	if len(requestIDs) > 0 {
		requestID = strings.TrimSpace(requestIDs[0])
	}
	identityID := ""
	succeeded := false
	defer func() {
		if requestID == "" || succeeded {
			return
		}
		metadata, _ := json.Marshal(map[string]string{"authenticationType": "local"})
		_ = service.store.Audit().Append(ctx, storage.AuditEvent{ID: service.newID(), IdentityID: identityID,
			Action: "admin.session.local.exchange", ResourceType: "admin-session", Outcome: "failure",
			RequestID: requestID, Metadata: metadata, CreatedAt: service.now().UTC()})
	}()
	stored, err := service.store.Credentials().GetPasswordByUsername(ctx, normalizeUsername(username))
	hash := service.dummyHash
	if err == nil {
		hash, identityID = stored.PasswordHash, stored.IdentityID
	}
	if !verifyPassword(password, hash) || err != nil {
		return User{}, ErrAuthenticationFailed
	}
	if !stored.Enabled {
		return User{}, ErrDisabled
	}
	user, err := service.user(ctx, stored)
	succeeded = err == nil
	return user, err
}

func (service *Service) List(ctx context.Context) ([]User, error) {
	identities, err := service.store.Identities().List(ctx, storage.IdentityListFilter{Type: "human", Limit: 100})
	if err != nil {
		return nil, err
	}
	users := make([]User, 0, len(identities))
	for _, identity := range identities {
		credential, credentialErr := service.store.Credentials().GetPasswordByIdentity(ctx, identity.ID)
		if errors.Is(credentialErr, storage.ErrNotFound) {
			continue
		}
		if credentialErr != nil {
			return nil, credentialErr
		}
		users = append(users, toUser(identity, credential))
	}
	return users, nil
}

func (service *Service) Get(ctx context.Context, identityID string) (User, error) {
	stored, err := service.store.Credentials().GetPasswordByIdentity(ctx, identityID)
	if err != nil {
		return User{}, err
	}
	return service.user(ctx, stored)
}

func (service *Service) SetEnabled(ctx context.Context, identityID string, enabled bool) error {
	now := service.now().UTC()
	return service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if err := repositories.Credentials().SetPasswordEnabled(ctx, identityID, enabled, now); err != nil {
			return err
		}
		if !enabled {
			_, err := repositories.OAuthSessions().RevokeIdentity(ctx, identityID, now)
			return err
		}
		return nil
	})
}

func (service *Service) SetPassword(ctx context.Context, identityID string, password []byte) error {
	if len(password) < minimumPasswordLen || len(password) > maximumPasswordLen {
		return ErrInvalidInput
	}
	hash, err := hashPassword(password, service.random)
	if err != nil {
		return err
	}
	now := service.now().UTC()
	return service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if err := repositories.Credentials().UpdatePassword(ctx, identityID, hash, now); err != nil {
			return err
		}
		_, err := repositories.OAuthSessions().RevokeIdentity(ctx, identityID, now)
		return err
	})
}

func (service *Service) user(ctx context.Context, stored storage.PasswordCredential) (User, error) {
	identity, err := service.store.Identities().GetByID(ctx, stored.IdentityID)
	if err != nil {
		return User{}, err
	}
	return toUser(identity, stored), nil
}

func toUser(identity storage.Identity, stored storage.PasswordCredential) User {
	return User{IdentityID: stored.IdentityID, Username: stored.Username, DisplayName: identity.DisplayName,
		Email: identity.PrimaryEmail, Enabled: stored.Enabled && identity.Status == "active",
		CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt}
}

func hashPassword(password []byte, source io.Reader) (string, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(source, salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey(password, salt, 3, 64*1024, 2, 32)
	defer clear(hash)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=2$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(password []byte, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 || memory != 64*1024 || iterations != 3 || parallelism != 2 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey(password, salt, iterations, memory, parallelism, uint32(len(expected)))
	defer clear(actual)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func normalizeUsername(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func validUsername(value string) bool {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\x00\r\n\t ") {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' || character == '@') {
			return false
		}
	}
	return true
}
