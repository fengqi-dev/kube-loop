package httpapi

import (
	"errors"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (api *readAPI) listOAuthClients(ctx *echo.Context) error {
	items, err := api.oauthRepositories.OAuthClients().
		List(ctx.Request().Context())
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	documents := make([]map[string]any, 0, len(items))
	for _, item := range items {
		documents = append(documents, oauthClientDocument(item))
	}
	return ctx.JSON(http.StatusOK, map[string]any{"items": documents})
}

func (api *readAPI) createOAuthClient(ctx *echo.Context) error {
	var input oauthClientInput
	if responseErr := bindJSON(ctx, &input); responseErr != nil {
		return responseErr.write(ctx)
	}
	if !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	client, err := oauthClientFromInput(input)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	var plaintext string
	err = api.oauthTransactions.WithinTransaction(
		ctx.Request().Context(),
		func(repositories storage.Repositories) error {
			if slices.Contains(client.GrantTypes, "client_credentials") {
				machine := storage.Identity{
					ID: uuid.NewString(), Type: "machine", DisplayName: client.Name,
					Status: statusActive, CreatedAt: client.CreatedAt, UpdatedAt: client.UpdatedAt,
				}
				identity, identityErr := repositories.Identities().
					Create(ctx.Request().Context(), machine)
				if identityErr != nil {
					return identityErr
				}
				client.MachineIdentityID = identity.ID
			}
			if err := repositories.OAuthClients().Create(ctx.Request().Context(), client); err != nil {
				return err
			}
			if !client.Public {
				var secretErr error
				plaintext, secretErr = generateOAuthClientSecret()
				if secretErr != nil {
					return secretErr
				}
				hash, hashErr := bcrypt.GenerateFromPassword(
					[]byte(plaintext),
					bcrypt.DefaultCost,
				)
				if hashErr != nil {
					return errors.New("hash OAuth client secret")
				}
				secret := storage.OAuthClientSecret{
					ClientID: client.ID, SecretHash: hash,
					CreatedAt: client.CreatedAt, UpdatedAt: client.UpdatedAt,
				}
				return repositories.OAuthClients().SetSecret(ctx.Request().Context(), secret)
			}
			return nil
		},
	)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(
		ctx.Request(),
		subjectFromRequest(ctx.Request()),
		"iam.oauth-client.create",
		"success",
	)
	document := oauthClientDocument(client)
	if plaintext != "" {
		document["clientSecret"] = plaintext
	}
	return ctx.JSON(http.StatusCreated, document)
}

func (api *readAPI) updateOAuthClient(ctx *echo.Context) error {
	var input oauthClientInput
	if responseErr := bindJSON(ctx, &input); responseErr != nil {
		return responseErr.write(ctx)
	}
	if !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	input.ID = ctx.Param("clientID")
	existingForAuthorization, err := api.oauthRepositories.OAuthClients().
		Get(ctx.Request().Context(), input.ID)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if responseErr := requireIAMETag(ctx, existingForAuthorization.UpdatedAt); responseErr != nil {
		return responseErr.write(ctx)
	}
	client, err := oauthClientFromInput(input)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	err = api.oauthTransactions.WithinTransaction(
		ctx.Request().Context(),
		func(repositories storage.Repositories) error {
			existing, getErr := repositories.OAuthClients().
				Get(ctx.Request().Context(), client.ID)
			if getErr != nil {
				return getErr
			}
			client.CreatedAt = existing.CreatedAt
			client.MachineIdentityID = existing.MachineIdentityID
			if slices.Contains(client.GrantTypes, "client_credentials") &&
				client.MachineIdentityID == "" {
				machine := storage.Identity{
					ID: uuid.NewString(), Type: "machine", DisplayName: client.Name,
					Status: statusActive, CreatedAt: client.UpdatedAt, UpdatedAt: client.UpdatedAt,
				}
				identity, identityErr := repositories.Identities().
					Create(ctx.Request().Context(), machine)
				if identityErr != nil {
					return identityErr
				}
				client.MachineIdentityID = identity.ID
			}
			return repositories.OAuthClients().
				Update(ctx.Request().Context(), client)
		},
	)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	stored, err := api.oauthRepositories.OAuthClients().
		Get(ctx.Request().Context(), client.ID)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(
		ctx.Request(),
		subjectFromRequest(ctx.Request()),
		"iam.oauth-client.update",
		"success",
	)
	ctx.Response().Header().Set("ETag", iamETag(stored.UpdatedAt))
	return ctx.JSON(http.StatusOK, oauthClientDocument(stored))
}

func (api *readAPI) deleteOAuthClient(ctx *echo.Context) error {
	client, err := api.oauthRepositories.OAuthClients().
		Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if !validChangeReason(ctx.Request().Header.Get("X-Kubeloop-Reason")) {
		return api.invalidIAMMutation(ctx)
	}
	if err := api.oauthRepositories.OAuthClients().Delete(ctx.Request().Context(), client.ID); err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(
		ctx.Request(),
		subjectFromRequest(ctx.Request()),
		"iam.oauth-client.delete",
		"success",
	)
	return ctx.NoContent(http.StatusNoContent)
}
