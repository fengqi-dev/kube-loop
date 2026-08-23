package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const TokenLifetime = 24 * time.Hour

type CompleteRequest struct {
	Token       string
	Username    string
	Password    []byte
	DisplayName string
	Email       string
	RequestID   string
}

// EnsureToken creates the only bootstrap token. The plaintext is returned only
// on the call that persisted it and can never be reconstructed from storage.
func (service *Service) EnsureToken(
	ctx context.Context,
) (string, time.Time, error) {
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
	token := storage.BootstrapToken{
		TokenHash: digest[:],
		CreatedAt: now,
		ExpiresAt: now.Add(TokenLifetime),
	}
	if err := service.store.BootstrapTokens().Create(ctx, token); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			stored, getErr := service.store.BootstrapTokens().Get(ctx)
			return "", stored.ExpiresAt, getErr
		}
		return "", time.Time{}, err
	}
	return plain, token.ExpiresAt, nil
}

func (service *Service) Complete(
	ctx context.Context,
	request CompleteRequest,
) (Result, error) {
	request.Token = strings.TrimSpace(request.Token)
	request.RequestID = strings.TrimSpace(request.RequestID)
	if request.Token == "" || uuid.Validate(request.RequestID) != nil {
		return Result{}, ErrInvalidRequest
	}
	digest := sha256.Sum256([]byte(request.Token))
	now := service.now().UTC()
	var result Result
	err := service.store.WithinTransaction(
		ctx,
		func(repositories storage.Repositories) error {
			if err := repositories.BootstrapTokens().Consume(ctx, digest[:], now); err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					return ErrInvalidToken
				}
				return err
			}
			var createErr error
			result, createErr = service.createInitialGraph(
				ctx,
				repositories,
				initialGraphRequest{
					Username:    request.Username,
					Password:    request.Password,
					DisplayName: request.DisplayName,
					Email:       request.Email,
					RequestID:   request.RequestID,
					AuditAction: "iam.bootstrap.complete",
					Now:         now,
				},
			)
			return createErr
		},
	)
	return result, err
}
