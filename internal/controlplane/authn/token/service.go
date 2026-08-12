package token

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
)

const (
	defaultAudience   = "kubeloop-api"
	defaultAccessTTL  = 5 * time.Minute
	defaultRefreshTTL = 30 * 24 * time.Hour
	defaultClockSkew  = 30 * time.Second
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrRevoked      = errors.New("token family revoked")
	ErrRefreshReuse = errors.New("refresh token reuse detected")
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type Config struct {
	Issuer     string
	Audience   string
	KeyID      string
	SigningKey ed25519.PrivateKey
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	ClockSkew  time.Duration
	Now        func() time.Time
	Random     func([]byte) error
}

type Service struct {
	store      Store
	issuer     string
	audience   string
	keyID      string
	publicKey  ed25519.PublicKey
	signer     jose.Signer
	accessTTL  time.Duration
	refreshTTL time.Duration
	clockSkew  time.Duration
	now        func() time.Time
	random     func([]byte) error
}

type Pair struct {
	TokenType        string
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

type AccessIdentity struct {
	Principal       storage.Principal
	FamilyID        string
	DeviceID        string
	TokenID         string
	AccessExpiresAt time.Time
}

type privateClaims struct {
	FamilyID string   `json:"fid"`
	DeviceID string   `json:"device_id"`
	Groups   []string `json:"groups,omitempty"`
}

func New(store Store, config Config) (*Service, error) {
	if store == nil {
		return nil, errors.New("token store is required")
	}
	config.Issuer = strings.TrimRight(strings.TrimSpace(config.Issuer), "/")
	issuer, err := url.Parse(config.Issuer)
	if err != nil || !issuer.IsAbs() || issuer.Scheme != "https" || issuer.Host == "" {
		return nil, errors.New("token issuer must be an absolute HTTPS URL")
	}
	config.Audience = strings.TrimSpace(config.Audience)
	if config.Audience == "" {
		config.Audience = defaultAudience
	}
	config.KeyID = strings.TrimSpace(config.KeyID)
	if config.KeyID == "" {
		return nil, errors.New("token signing key ID is required")
	}
	if len(config.SigningKey) != ed25519.PrivateKeySize {
		return nil, errors.New("token signing key must be an Ed25519 private key")
	}
	if config.AccessTTL <= 0 {
		config.AccessTTL = defaultAccessTTL
	}
	if config.RefreshTTL <= 0 {
		config.RefreshTTL = defaultRefreshTTL
	}
	if config.AccessTTL > 15*time.Minute || config.RefreshTTL > 90*24*time.Hour {
		return nil, errors.New("token lifetime exceeds security limit")
	}
	if config.ClockSkew <= 0 {
		config.ClockSkew = defaultClockSkew
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = func(buffer []byte) error {
			_, err := rand.Read(buffer)
			return err
		}
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.EdDSA, Key: config.SigningKey},
		new(jose.SignerOptions).WithType("JWT").WithHeader("kid", config.KeyID),
	)
	if err != nil {
		return nil, errors.New("initialize token signer")
	}
	publicKey, ok := config.SigningKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("derive token verification key")
	}
	return &Service{
		store: store, issuer: config.Issuer, audience: config.Audience, keyID: config.KeyID,
		publicKey: publicKey, signer: signer, accessTTL: config.AccessTTL,
		refreshTTL: config.RefreshTTL, clockSkew: config.ClockSkew,
		now: config.Now, random: config.Random,
	}, nil
}

func (service *Service) Issue(ctx context.Context, principal storage.Principal, deviceID string) (Pair, error) {
	deviceID = strings.TrimSpace(deviceID)
	if _, err := uuid.Parse(principal.ID); err != nil || deviceID == "" || len(deviceID) > 128 {
		return Pair{}, errors.New("valid principal and device IDs are required")
	}
	now := service.now().UTC()
	refreshToken, refreshHash, err := service.newRefreshToken()
	if err != nil {
		return Pair{}, errors.New("generate refresh token")
	}
	family := storage.TokenFamily{
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: deviceID,
		RefreshTokenHash: refreshHash, CreatedAt: now, ExpiresAt: now.Add(service.refreshTTL),
	}
	accessToken, accessExpiry, err := service.signAccess(principal, family, now)
	if err != nil {
		return Pair{}, err
	}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		if err := repositories.TokenFamilies().Create(ctx, family); err != nil {
			return err
		}
		return repositories.RefreshTokens().Create(ctx, storage.RefreshTokenRecord{
			TokenHash: refreshHash, FamilyID: family.ID, CreatedAt: now,
		})
	})
	if err != nil {
		return Pair{}, errors.New("persist token family")
	}
	return Pair{
		TokenType: "Bearer", AccessToken: accessToken, AccessExpiresAt: accessExpiry,
		RefreshToken: refreshToken, RefreshExpiresAt: family.ExpiresAt,
	}, nil
}

func (service *Service) Refresh(ctx context.Context, refreshToken string) (Pair, error) {
	currentHash, err := decodeAndHashRefreshToken(refreshToken)
	if err != nil {
		return Pair{}, ErrInvalidToken
	}
	nextToken, nextHash, err := service.newRefreshToken()
	if err != nil {
		return Pair{}, errors.New("generate refresh token")
	}
	now := service.now().UTC()
	var family storage.TokenFamily
	var principal storage.Principal
	reuse := false
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		reuse = false
		record, err := repositories.RefreshTokens().GetByHash(ctx, currentHash)
		if err != nil {
			return ErrInvalidToken
		}
		family, err = repositories.TokenFamilies().GetByID(ctx, record.FamilyID)
		if err != nil {
			return ErrInvalidToken
		}
		if record.Status != "active" {
			if err := repositories.TokenFamilies().Revoke(ctx, family.ID, now); err != nil {
				return err
			}
			reuse = true
			return nil
		}
		if family.RevokedAt != nil || !family.ExpiresAt.After(now) {
			return ErrRevoked
		}
		principal, err = repositories.Principals().GetByID(ctx, family.PrincipalID)
		if err != nil {
			return ErrInvalidToken
		}
		if err := repositories.RefreshTokens().MarkUsed(ctx, currentHash, now); err != nil {
			if errors.Is(err, storage.ErrConflict) {
				if err := repositories.TokenFamilies().Revoke(ctx, family.ID, now); err != nil {
					return err
				}
				reuse = true
				return nil
			}
			return err
		}
		if err := repositories.TokenFamilies().RotateHash(ctx, family.ID, currentHash, nextHash); err != nil {
			if errors.Is(err, storage.ErrConflict) {
				if err := repositories.TokenFamilies().Revoke(ctx, family.ID, now); err != nil {
					return err
				}
				reuse = true
				return nil
			}
			return err
		}
		return repositories.RefreshTokens().Create(ctx, storage.RefreshTokenRecord{
			TokenHash: nextHash, FamilyID: family.ID, CreatedAt: now,
		})
	})
	if err != nil {
		return Pair{}, err
	}
	if reuse {
		return Pair{}, ErrRefreshReuse
	}
	family.RefreshTokenHash = nextHash
	accessToken, accessExpiry, err := service.signAccess(principal, family, now)
	if err != nil {
		return Pair{}, err
	}
	return Pair{
		TokenType: "Bearer", AccessToken: accessToken, AccessExpiresAt: accessExpiry,
		RefreshToken: nextToken, RefreshExpiresAt: family.ExpiresAt,
	}, nil
}

func (service *Service) Revoke(ctx context.Context, refreshToken string) error {
	hash, err := decodeAndHashRefreshToken(refreshToken)
	if err != nil {
		return ErrInvalidToken
	}
	record, err := service.store.RefreshTokens().GetByHash(ctx, hash)
	if err != nil {
		return ErrInvalidToken
	}
	if err := service.store.TokenFamilies().Revoke(ctx, record.FamilyID, service.now().UTC()); err != nil {
		return errors.New("revoke token family")
	}
	return nil
}

func (service *Service) Authenticate(ctx context.Context, accessToken string) (AccessIdentity, error) {
	parsed, err := jwt.ParseSigned(strings.TrimSpace(accessToken), []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return AccessIdentity{}, ErrInvalidToken
	}
	if len(parsed.Headers) != 1 || parsed.Headers[0].Algorithm != string(jose.EdDSA) || parsed.Headers[0].KeyID != service.keyID {
		return AccessIdentity{}, ErrInvalidToken
	}
	tokenType, ok := parsed.Headers[0].ExtraHeaders[jose.HeaderType].(string)
	if !ok || tokenType != "JWT" {
		return AccessIdentity{}, ErrInvalidToken
	}
	var standard jwt.Claims
	var private privateClaims
	if err := parsed.Claims(service.publicKey, &standard, &private); err != nil {
		return AccessIdentity{}, ErrInvalidToken
	}
	now := service.now().UTC()
	if err := standard.ValidateWithLeeway(jwt.Expected{
		Issuer: service.issuer, AnyAudience: jwt.Audience{service.audience}, Time: now,
	}, service.clockSkew); err != nil {
		return AccessIdentity{}, ErrInvalidToken
	}
	if standard.Expiry == nil || standard.Expiry.Time().IsZero() || standard.ID == "" {
		return AccessIdentity{}, ErrInvalidToken
	}
	if _, err := uuid.Parse(standard.Subject); err != nil {
		return AccessIdentity{}, ErrInvalidToken
	}
	family, err := service.store.TokenFamilies().GetByID(ctx, private.FamilyID)
	if err != nil || family.PrincipalID != standard.Subject || family.DeviceID != private.DeviceID {
		return AccessIdentity{}, ErrInvalidToken
	}
	if family.RevokedAt != nil || !family.ExpiresAt.After(now) {
		return AccessIdentity{}, ErrRevoked
	}
	principal, err := service.store.Principals().GetByID(ctx, standard.Subject)
	if err != nil {
		return AccessIdentity{}, ErrInvalidToken
	}
	return AccessIdentity{
		Principal: principal, FamilyID: family.ID, DeviceID: family.DeviceID, TokenID: standard.ID,
		AccessExpiresAt: standard.Expiry.Time(),
	}, nil
}

func (service *Service) signAccess(
	principal storage.Principal,
	family storage.TokenFamily,
	now time.Time,
) (string, time.Time, error) {
	expiresAt := now.Add(service.accessTTL)
	serialized, err := jwt.Signed(service.signer).Claims(jwt.Claims{
		Issuer: service.issuer, Subject: principal.ID, Audience: jwt.Audience{service.audience},
		Expiry: jwt.NewNumericDate(expiresAt), NotBefore: jwt.NewNumericDate(now),
		IssuedAt: jwt.NewNumericDate(now), ID: uuid.NewString(),
	}).Claims(privateClaims{
		FamilyID: family.ID, DeviceID: family.DeviceID, Groups: principal.Groups,
	}).Serialize()
	if err != nil {
		return "", time.Time{}, errors.New("sign access token")
	}
	return serialized, expiresAt, nil
}

func (service *Service) newRefreshToken() (string, []byte, error) {
	buffer := make([]byte, 32)
	if err := service.random(buffer); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(buffer)
	hash := sha256.Sum256([]byte(raw))
	return raw, hash[:], nil
}

func decodeAndHashRefreshToken(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return nil, ErrInvalidToken
	}
	hash := sha256.Sum256([]byte(raw))
	return hash[:], nil
}
