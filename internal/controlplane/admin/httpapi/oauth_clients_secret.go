package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (api *readAPI) rotateOAuthClientSecret(ctx *echo.Context) error {
	client, err := api.oauthRepositories.OAuthClients().
		Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if responseErr := bindJSON(ctx, &input); responseErr != nil {
		return responseErr.write(ctx)
	}
	if !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	if client.Public {
		return api.oauthClientError(
			ctx,
			errors.New("public OAuth clients do not have secrets"),
		)
	}
	plaintext, err := generateOAuthClientSecret()
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	hash, err := bcrypt.GenerateFromPassword(
		[]byte(plaintext),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return api.oauthClientError(ctx, errors.New("hash OAuth client secret"))
	}
	now := time.Now().UTC()
	secret := storage.OAuthClientSecret{
		ClientID: client.ID, SecretHash: hash, CreatedAt: now, UpdatedAt: now,
	}
	err = api.oauthRepositories.OAuthClients().
		SetSecret(ctx.Request().Context(), secret)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(
		ctx.Request(),
		subjectFromRequest(ctx.Request()),
		"iam.oauth-client.secret.rotate",
		"success",
	)
	return ctx.JSON(
		http.StatusOK,
		map[string]any{"clientId": client.ID, "clientSecret": plaintext},
	)
}

func generateOAuthClientSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate OAuth client secret")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
