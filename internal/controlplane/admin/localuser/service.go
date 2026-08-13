// Package localuser manages Management Plane password users and optional MFA.
package localuser

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/argon2"
)

const (
	ProviderID         = "local"
	minimumPasswordLen = 12
	maximumPasswordLen = 1024
	recoveryCodeCount  = 10
	enrollmentTTL      = 10 * time.Minute
)

var (
	ErrAuthenticationFailed = errors.New("local administrator authentication failed")
	ErrInvalidInput         = errors.New("local administrator input is invalid")
	ErrDisabled             = errors.New("local administrator is disabled")
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type User struct {
	PrincipalID string    `json:"principalId"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Email       string    `json:"email,omitempty"`
	Enabled     bool      `json:"enabled"`
	MFAEnabled  bool      `json:"mfaEnabled"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type CreateRequest struct {
	Username    string
	Password    []byte
	DisplayName string
	Email       string
}

type Enrollment struct {
	Secret          string   `json:"secret"`
	ProvisioningURI string   `json:"provisioningUri"`
	QRCodeDataURL   string   `json:"qrCodeDataUrl"`
	EnrollmentToken string   `json:"enrollmentToken"`
	RecoveryCodes   []string `json:"recoveryCodes,omitempty"`
}

type Service struct {
	store     Store
	aead      cipher.AEAD
	issuer    string
	random    io.Reader
	now       func() time.Time
	newID     func() string
	dummyHash string
}

func New(store Store, encryptionKey []byte, issuer string) (*Service, error) {
	if store == nil || len(encryptionKey) != 32 || strings.TrimSpace(issuer) == "" {
		return nil, errors.New("local administrator storage, issuer, and 32-byte MFA key are required")
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, errors.New("initialize local administrator encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("initialize local administrator encryption")
	}
	dummy, err := hashPassword([]byte("not-a-real-password"), rand.Reader)
	if err != nil {
		return nil, errors.New("initialize password verifier")
	}
	return &Service{store: store, aead: aead, issuer: strings.TrimSpace(issuer), random: rand.Reader,
		now: time.Now, newID: uuid.NewString, dummyHash: dummy}, nil
}

func (service *Service) EnsureInitial(ctx context.Context, request CreateRequest) (User, bool, error) {
	username := normalizeUsername(request.Username)
	if stored, err := service.store.LocalAdminUsers().GetByUsername(ctx, username); err == nil {
		user, convertErr := service.user(ctx, stored)
		return user, false, convertErr
	} else if !errors.Is(err, storage.ErrNotFound) {
		return User{}, false, err
	}
	user, err := service.Create(ctx, request)
	if errors.Is(err, storage.ErrConflict) {
		stored, lookupErr := service.store.LocalAdminUsers().GetByUsername(ctx, username)
		if lookupErr != nil {
			return User{}, false, lookupErr
		}
		user, lookupErr = service.user(ctx, stored)
		return user, false, lookupErr
	}
	return user, err == nil, err
}

func (service *Service) Create(ctx context.Context, request CreateRequest) (User, error) {
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
	now, principalID := service.now().UTC(), service.newID()
	principal := storage.Principal{ID: principalID, Provider: ProviderID, ExternalID: request.Username,
		DisplayName: request.DisplayName, Email: request.Email, Groups: []string{}, CreatedAt: now, UpdatedAt: now}
	if principal.DisplayName == "" {
		principal.DisplayName = request.Username
	}
	var result User
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		stored, createErr := repositories.Principals().Upsert(ctx, principal)
		if createErr != nil {
			return createErr
		}
		if stored.ID != principalID {
			return storage.ErrConflict
		}
		if createErr := repositories.LocalAdminUsers().Create(ctx, storage.LocalAdminUser{
			PrincipalID: stored.ID, Username: request.Username, PasswordHash: passwordHash,
			Enabled: true, CreatedAt: now, UpdatedAt: now,
		}); createErr != nil {
			return createErr
		}
		result = toUser(stored, storage.LocalAdminUser{PrincipalID: stored.ID, Username: request.Username,
			Enabled: true, CreatedAt: now, UpdatedAt: now})
		return nil
	})
	return result, err
}

func (service *Service) Authenticate(
	ctx context.Context,
	username string,
	password []byte,
	secondFactor string,
	requestIDs ...string,
) (User, error) {
	requestID := ""
	if len(requestIDs) > 0 {
		requestID = strings.TrimSpace(requestIDs[0])
	}
	principalID := ""
	succeeded := false
	defer func() {
		if requestID == "" || succeeded {
			return
		}
		metadata, _ := json.Marshal(map[string]string{"authenticationType": "local"})
		_ = service.store.Audit().Append(ctx, storage.AuditEvent{
			ID: service.newID(), PrincipalID: principalID, Action: "admin.session.local.exchange",
			ResourceType: "admin-session", Outcome: "failure", RequestID: requestID,
			Metadata: metadata, CreatedAt: service.now().UTC(),
		})
	}()
	stored, err := service.store.LocalAdminUsers().GetByUsername(ctx, normalizeUsername(username))
	hash := service.dummyHash
	if err == nil {
		hash = stored.PasswordHash
		principalID = stored.PrincipalID
	}
	passwordOK := verifyPassword(password, hash)
	if err != nil || !passwordOK {
		return User{}, ErrAuthenticationFailed
	}
	if !stored.Enabled {
		return User{}, ErrDisabled
	}
	if len(stored.TOTPSecretEncrypted) > 0 {
		secret, decryptErr := service.decrypt(stored.TOTPSecretEncrypted, []byte(stored.PrincipalID))
		if decryptErr != nil {
			return User{}, ErrAuthenticationFailed
		}
		valid := totp.Validate(strings.TrimSpace(secondFactor), string(secret))
		clear(secret)
		if !valid {
			digest := recoveryHash(secondFactor)
			if consumeErr := service.store.AdminRecoveryCodes().Consume(ctx, stored.PrincipalID, digest[:]); consumeErr != nil {
				return User{}, ErrAuthenticationFailed
			}
		}
	}
	user, err := service.user(ctx, stored)
	succeeded = err == nil
	return user, err
}

func (service *Service) List(ctx context.Context) ([]User, error) {
	stored, err := service.store.LocalAdminUsers().List(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]User, 0, len(stored))
	for _, item := range stored {
		user, convertErr := service.user(ctx, item)
		if convertErr != nil {
			return nil, convertErr
		}
		users = append(users, user)
	}
	return users, nil
}

func (service *Service) Get(ctx context.Context, principalID string) (User, error) {
	stored, err := service.store.LocalAdminUsers().GetByPrincipalID(ctx, principalID)
	if err != nil {
		return User{}, err
	}
	return service.user(ctx, stored)
}

func (service *Service) SetEnabled(ctx context.Context, principalID string, enabled bool) error {
	now := service.now().UTC()
	return service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if err := repositories.LocalAdminUsers().UpdateEnabled(ctx, principalID, enabled, now); err != nil {
			return err
		}
		if !enabled {
			_, err := repositories.OAuthSessions().RevokePrincipal(ctx, principalID, now)
			return err
		}
		return nil
	})
}

func (service *Service) BootstrapComplete(ctx context.Context, principalID string) (bool, error) {
	user, err := service.store.LocalAdminUsers().GetByPrincipalID(ctx, principalID)
	return user.BootstrapComplete, err
}

func (service *Service) MarkBootstrapComplete(ctx context.Context, principalID string) error {
	return service.store.LocalAdminUsers().MarkBootstrapComplete(ctx, principalID, service.now().UTC())
}

func (service *Service) SetPassword(ctx context.Context, principalID string, password []byte) error {
	if len(password) < minimumPasswordLen || len(password) > maximumPasswordLen {
		return ErrInvalidInput
	}
	hash, err := hashPassword(password, service.random)
	if err != nil {
		return err
	}
	now := service.now().UTC()
	return service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if err := repositories.LocalAdminUsers().UpdatePassword(ctx, principalID, hash, now); err != nil {
			return err
		}
		_, err := repositories.OAuthSessions().RevokePrincipal(ctx, principalID, now)
		return err
	})
}

func (service *Service) StartTOTP(ctx context.Context, principalID string) (Enrollment, error) {
	stored, err := service.store.LocalAdminUsers().GetByPrincipalID(ctx, principalID)
	if err != nil || !stored.Enabled || len(stored.TOTPSecretEncrypted) != 0 {
		return Enrollment{}, ErrInvalidInput
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: service.issuer, AccountName: stored.Username})
	if err != nil {
		return Enrollment{}, errors.New("generate TOTP enrollment")
	}
	qrImage, err := key.Image(256, 256)
	if err != nil {
		return Enrollment{}, errors.New("generate TOTP QR code")
	}
	var qrPNG bytes.Buffer
	if err := png.Encode(&qrPNG, qrImage); err != nil {
		return Enrollment{}, errors.New("encode TOTP QR code")
	}
	payload, err := json.Marshal(struct {
		PrincipalID string    `json:"principalId"`
		Secret      string    `json:"secret"`
		ExpiresAt   time.Time `json:"expiresAt"`
	}{principalID, key.Secret(), service.now().UTC().Add(enrollmentTTL)})
	if err != nil {
		return Enrollment{}, errors.New("encode TOTP enrollment")
	}
	token, err := service.encrypt(payload, []byte("totp-enrollment"))
	clear(payload)
	if err != nil {
		return Enrollment{}, err
	}
	return Enrollment{Secret: key.Secret(), ProvisioningURI: key.URL(),
		QRCodeDataURL:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(qrPNG.Bytes()),
		EnrollmentToken: base64.RawURLEncoding.EncodeToString(token)}, nil
}

func (service *Service) ConfirmTOTP(ctx context.Context, principalID, enrollmentToken, code string) ([]string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(enrollmentToken))
	if err != nil {
		return nil, ErrInvalidInput
	}
	payload, err := service.decrypt(raw, []byte("totp-enrollment"))
	clear(raw)
	if err != nil {
		return nil, ErrInvalidInput
	}
	defer clear(payload)
	var enrollment struct {
		PrincipalID string    `json:"principalId"`
		Secret      string    `json:"secret"`
		ExpiresAt   time.Time `json:"expiresAt"`
	}
	if json.Unmarshal(payload, &enrollment) != nil || enrollment.PrincipalID != principalID ||
		!enrollment.ExpiresAt.After(service.now().UTC()) || !totp.Validate(strings.TrimSpace(code), enrollment.Secret) {
		return nil, ErrInvalidInput
	}
	encrypted, err := service.encrypt([]byte(enrollment.Secret), []byte(principalID))
	if err != nil {
		return nil, err
	}
	codes, hashes, err := service.newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if updateErr := repositories.LocalAdminUsers().UpdateTOTP(ctx, principalID, encrypted, now); updateErr != nil {
			return updateErr
		}
		return repositories.AdminRecoveryCodes().Replace(ctx, principalID, hashes, now)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func (service *Service) RegenerateRecoveryCodes(ctx context.Context, principalID, code string) ([]string, error) {
	stored, err := service.store.LocalAdminUsers().GetByPrincipalID(ctx, principalID)
	if err != nil || len(stored.TOTPSecretEncrypted) == 0 {
		return nil, ErrInvalidInput
	}
	secret, err := service.decrypt(stored.TOTPSecretEncrypted, []byte(principalID))
	if err != nil || !totp.Validate(strings.TrimSpace(code), string(secret)) {
		clear(secret)
		return nil, ErrAuthenticationFailed
	}
	clear(secret)
	codes, hashes, err := service.newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		return repositories.AdminRecoveryCodes().Replace(ctx, principalID, hashes, service.now().UTC())
	}); err != nil {
		return nil, err
	}
	return codes, nil
}

func (service *Service) DisableTOTP(ctx context.Context, principalID, password, code string) error {
	stored, err := service.store.LocalAdminUsers().GetByPrincipalID(ctx, principalID)
	if err != nil || !verifyPassword([]byte(password), stored.PasswordHash) || len(stored.TOTPSecretEncrypted) == 0 {
		return ErrAuthenticationFailed
	}
	secret, err := service.decrypt(stored.TOTPSecretEncrypted, []byte(principalID))
	if err != nil || !totp.Validate(strings.TrimSpace(code), string(secret)) {
		clear(secret)
		return ErrAuthenticationFailed
	}
	clear(secret)
	return service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if updateErr := repositories.LocalAdminUsers().UpdateTOTP(ctx, principalID, nil, service.now().UTC()); updateErr != nil {
			return updateErr
		}
		return repositories.AdminRecoveryCodes().DeleteByPrincipal(ctx, principalID)
	})
}

func (service *Service) user(ctx context.Context, stored storage.LocalAdminUser) (User, error) {
	principal, err := service.store.Principals().GetByID(ctx, stored.PrincipalID)
	if err != nil {
		return User{}, err
	}
	return toUser(principal, stored), nil
}

func toUser(principal storage.Principal, stored storage.LocalAdminUser) User {
	return User{PrincipalID: stored.PrincipalID, Username: stored.Username, DisplayName: principal.DisplayName,
		Email: principal.Email, Enabled: stored.Enabled, MFAEnabled: len(stored.TOTPSecretEncrypted) > 0,
		CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt}
}

func (service *Service) newRecoveryCodes() ([]string, [][]byte, error) {
	codes, hashes := make([]string, recoveryCodeCount), make([][]byte, recoveryCodeCount)
	for index := range codes {
		raw := make([]byte, 10)
		if _, err := io.ReadFull(service.random, raw); err != nil {
			return nil, nil, errors.New("generate recovery codes")
		}
		encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
		clear(raw)
		codes[index] = encoded[:8] + "-" + encoded[8:]
		digest := recoveryHash(codes[index])
		hashes[index] = append([]byte(nil), digest[:]...)
	}
	return codes, hashes, nil
}

func (service *Service) encrypt(plaintext, additional []byte) ([]byte, error) {
	nonce := make([]byte, service.aead.NonceSize())
	if _, err := io.ReadFull(service.random, nonce); err != nil {
		return nil, errors.New("generate encryption nonce")
	}
	return service.aead.Seal(nonce, nonce, plaintext, additional), nil
}

func (service *Service) decrypt(ciphertext, additional []byte) ([]byte, error) {
	if len(ciphertext) < service.aead.NonceSize()+service.aead.Overhead() {
		return nil, errors.New("encrypted value is invalid")
	}
	nonce := ciphertext[:service.aead.NonceSize()]
	plaintext, err := service.aead.Open(nil, nonce, ciphertext[service.aead.NonceSize():], additional)
	if err != nil {
		return nil, errors.New("decrypt local administrator value")
	}
	return plaintext, nil
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
	var memory uint32
	var iterations uint32
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

func recoveryHash(code string) [sha256.Size]byte {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	return sha256.Sum256([]byte(normalized))
}
