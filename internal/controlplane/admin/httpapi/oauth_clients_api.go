package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"
)

type oauthClientInput struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Public        bool     `json:"public"`
	RedirectURIs  []string `json:"redirectUris"`
	GrantTypes    []string `json:"grantTypes"`
	ResponseTypes []string `json:"responseTypes"`
	Scopes        []string `json:"scopes"`
	Trusted       bool     `json:"trusted"`
	Enabled       bool     `json:"enabled"`
}

func (api *readAPI) listOAuthClients(ctx *echo.Context) error {
	items, err := api.oauthRepositories.OAuthClients().List(ctx.Request().Context())
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
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) {
		return nil
	}
	client, err := oauthClientFromInput(input)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	var plaintext string
	err = api.oauthTransactions.WithinTransaction(ctx.Request().Context(), func(repositories storage.Repositories) error {
		if slices.Contains(client.GrantTypes, "client_credentials") {
			principal, principalErr := repositories.Principals().Upsert(ctx.Request().Context(), storage.Principal{ID: uuid.NewString(), Provider: "oauth-client", ExternalID: client.ID, DisplayName: client.Name, CreatedAt: client.CreatedAt, UpdatedAt: client.UpdatedAt})
			if principalErr != nil {
				return principalErr
			}
			client.MachinePrincipalID = principal.ID
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
			hash, hashErr := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
			if hashErr != nil {
				return errors.New("hash OAuth client secret")
			}
			return repositories.OAuthClients().SetSecret(ctx.Request().Context(), storage.OAuthClientSecret{ClientID: client.ID, SecretHash: hash, CreatedAt: client.CreatedAt, UpdatedAt: client.UpdatedAt})
		}
		return nil
	})
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	document := oauthClientDocument(client)
	if plaintext != "" {
		document["clientSecret"] = plaintext
	}
	return ctx.JSON(http.StatusCreated, document)
}

func (api *readAPI) updateOAuthClient(ctx *echo.Context) error {
	var input oauthClientInput
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) {
		return nil
	}
	input.ID = ctx.Param("clientID")
	client, err := oauthClientFromInput(input)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	err = api.oauthTransactions.WithinTransaction(ctx.Request().Context(), func(repositories storage.Repositories) error {
		existing, getErr := repositories.OAuthClients().Get(ctx.Request().Context(), client.ID)
		if getErr != nil {
			return getErr
		}
		client.CreatedAt = existing.CreatedAt
		client.MachinePrincipalID = existing.MachinePrincipalID
		if slices.Contains(client.GrantTypes, "client_credentials") && client.MachinePrincipalID == "" {
			principal, principalErr := repositories.Principals().Upsert(ctx.Request().Context(), storage.Principal{ID: uuid.NewString(), Provider: "oauth-client", ExternalID: client.ID, DisplayName: client.Name, CreatedAt: client.UpdatedAt, UpdatedAt: client.UpdatedAt})
			if principalErr != nil {
				return principalErr
			}
			client.MachinePrincipalID = principal.ID
		}
		return repositories.OAuthClients().Update(ctx.Request().Context(), client)
	})
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	stored, err := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), client.ID)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	return ctx.JSON(http.StatusOK, oauthClientDocument(stored))
}

func (api *readAPI) rotateOAuthClientSecret(ctx *echo.Context) error {
	client, err := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if client.Public {
		return api.oauthClientError(ctx, errors.New("public OAuth clients do not have secrets"))
	}
	plaintext, err := generateOAuthClientSecret()
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return api.oauthClientError(ctx, errors.New("hash OAuth client secret"))
	}
	now := time.Now().UTC()
	err = api.oauthRepositories.OAuthClients().SetSecret(ctx.Request().Context(), storage.OAuthClientSecret{ClientID: client.ID, SecretHash: hash, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	return ctx.JSON(http.StatusOK, map[string]any{"clientId": client.ID, "clientSecret": plaintext})
}

func (api *readAPI) setOAuthClientEnabled(ctx *echo.Context) error {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) {
		return nil
	}
	client, err := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	client.Enabled = input.Enabled
	client.UpdatedAt = time.Now().UTC()
	if err := api.oauthRepositories.OAuthClients().Update(ctx.Request().Context(), client); err != nil {
		return api.oauthClientError(ctx, err)
	}
	stored, _ := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), client.ID)
	return ctx.JSON(http.StatusOK, oauthClientDocument(stored))
}

func (api *readAPI) deleteOAuthClient(ctx *echo.Context) error {
	if err := api.oauthRepositories.OAuthClients().Delete(ctx.Request().Context(), ctx.Param("clientID")); err != nil {
		return api.oauthClientError(ctx, err)
	}
	return ctx.NoContent(http.StatusNoContent)
}
func (api *readAPI) revokeOAuthConsent(ctx *echo.Context) error {
	if err := api.oauthRepositories.OAuthConsents().RevokeClient(ctx.Request().Context(), ctx.Param("principalID"), ctx.Param("clientID")); err != nil {
		return api.oauthClientError(ctx, err)
	}
	return ctx.NoContent(http.StatusNoContent)
}

func oauthClientFromInput(input oauthClientInput) (storage.OAuthClient, error) {
	now := time.Now().UTC()
	client := storage.OAuthClient{ID: strings.TrimSpace(input.ID), Name: strings.TrimSpace(input.Name), Public: input.Public, RedirectURIs: input.RedirectURIs, GrantTypes: input.GrantTypes, ResponseTypes: input.ResponseTypes, Scopes: input.Scopes, Trusted: input.Trusted, Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now}
	allowedGrants := []string{"authorization_code", "refresh_token", "implicit", "password", "client_credentials"}
	allowedResponses := []string{"code", "token", "id_token", "id_token token", "code token", "code id_token", "code id_token token"}
	for _, grant := range client.GrantTypes {
		if !slices.Contains(allowedGrants, grant) {
			return storage.OAuthClient{}, errors.New("OAuth client grant type is invalid")
		}
	}
	for _, response := range client.ResponseTypes {
		if !slices.Contains(allowedResponses, response) {
			return storage.OAuthClient{}, errors.New("OAuth client response type is invalid")
		}
	}
	if slices.Contains(client.GrantTypes, "authorization_code") && !slices.Contains(client.ResponseTypes, "code") {
		return storage.OAuthClient{}, errors.New("authorization code clients require the code response type")
	}
	implicitResponse := false
	hybridResponse := false
	for _, response := range client.ResponseTypes {
		parts := strings.Fields(response)
		implicitResponse = implicitResponse || (!slices.Contains(parts, "code") && (slices.Contains(parts, "token") || slices.Contains(parts, "id_token")))
		hybridResponse = hybridResponse || (slices.Contains(parts, "code") && (slices.Contains(parts, "token") || slices.Contains(parts, "id_token")))
	}
	if (implicitResponse || hybridResponse) && !slices.Contains(client.GrantTypes, "implicit") {
		return storage.OAuthClient{}, errors.New("implicit and hybrid response types require the implicit grant")
	}
	if slices.Contains(client.GrantTypes, "refresh_token") && !slices.Contains(client.Scopes, "offline_access") {
		return storage.OAuthClient{}, errors.New("refresh token clients require the offline_access scope")
	}
	if slices.Contains(client.GrantTypes, "client_credentials") {
		for _, scope := range []string{"openid", "profile", "email", "offline_access"} {
			if slices.Contains(client.Scopes, scope) {
				return storage.OAuthClient{}, errors.New("client credentials clients cannot allow identity scopes")
			}
		}
	}
	if client.Public && (slices.Contains(client.GrantTypes, "password") || slices.Contains(client.GrantTypes, "client_credentials")) {
		return storage.OAuthClient{}, errors.New("public OAuth clients cannot use credential grants")
	}
	for _, redirect := range client.RedirectURIs {
		parsed, err := url.Parse(redirect)
		if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1" || parsed.Hostname() == "localhost"))) {
			return storage.OAuthClient{}, errors.New("OAuth client redirect URI is invalid")
		}
	}
	return client, nil
}

func oauthClientDocument(client storage.OAuthClient) map[string]any {
	return map[string]any{"id": client.ID, "name": client.Name, "public": client.Public, "redirectUris": client.RedirectURIs, "grantTypes": client.GrantTypes, "responseTypes": client.ResponseTypes, "scopes": client.Scopes, "trusted": client.Trusted, "enabled": client.Enabled, "builtin": client.Builtin, "machinePrincipalId": client.MachinePrincipalID, "createdAt": client.CreatedAt, "updatedAt": client.UpdatedAt}
}
func generateOAuthClientSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate OAuth client secret")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func (api *readAPI) oauthClientError(ctx *echo.Context, err error) error {
	status, code, message := http.StatusBadRequest, "invalid_request", "OAuth client request is invalid"
	if errors.Is(err, storage.ErrNotFound) {
		status, code, message = http.StatusNotFound, "not_found", "OAuth client was not found"
	} else if errors.Is(err, storage.ErrConflict) {
		status, code, message = http.StatusConflict, "conflict", "OAuth client already exists"
	}
	writeError(ctx.Response(), status, code, message, requestID(ctx.Request()))
	return nil
}
